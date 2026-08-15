from __future__ import annotations

import json
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import cast

from sqlalchemy import func, select
from sqlalchemy.orm import Session, selectinload

from music_ingest.models import SourceRecord
from music_ingest.models.library import (
    EffectiveSourceDecisionRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
)
from music_ingest.quality_policy import (
    DecisionReason,
    ExistingDecision,
    QualityCandidate,
    QualityDecision,
    QualityTuple,
    evaluate,
)


def new_library_record(session: Session, now: datetime | None = None) -> LibraryRecord:
    """Create one stable record before any source file is processed."""
    timestamp = now or datetime.now(UTC)
    record = LibraryRecord(id=f'record-{uuid.uuid4().hex}', created_at=timestamp, updated_at=timestamp)
    session.add(record)
    session.flush()
    return record


def ensure_source_record(session: Session, source: SourceRecord, now: datetime) -> LibraryRecord:
    """Return the stable record for a source, creating one for legacy intake rows."""
    if source.library_record is not None:
        return source.library_record
    record = new_library_record(session, now)
    source.library_record = record
    session.flush()
    return record


def attach_source(
    session: Session,
    source_id: str,
    library_record_id: str,
    *,
    reason: str = 'source_attached',
    now: datetime | None = None,
) -> SourceRecord:
    """Attach an immutable source version to an existing stable library record."""
    source = session.scalar(select(SourceRecord).where(SourceRecord.id == source_id))
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if source is None or record is None:
        raise LookupError(source_id if source is None else library_record_id)
    timestamp = now or datetime.now(UTC)
    source.library_record = record
    source.disappeared_at = None
    record.source_state = 'present'
    record.updated_at = timestamp
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source.id,
            kind='source_replaced' if reason == 'source_replaced' else 'source_attached',
            state='present',
            reason=reason,
            details_json='{}',
            created_at=timestamp,
        )
    )
    session.flush()
    _ = reevaluate_effective_source_decision(session, record.id, timestamp)
    return source


def record_publication(
    session: Session,
    library_record_id: str,
    source_id: str,
    published_path: Path,
    content_sha256: str,
    metadata_revision_id: int | None,
    now: datetime,
) -> LibraryPublicationRecord:
    """Append a current publication version and supersede the previous one."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    for previous in record.publications:
        if previous.state == 'current':
            previous.state = 'superseded'
    publication = LibraryPublicationRecord(
        id=f'publication-{uuid.uuid4().hex}',
        library_record_id=record.id,
        source_id=source_id,
        path=str(published_path),
        format_name=published_path.suffix.removeprefix('.'),
        content_sha256=content_sha256,
        metadata_revision_id=metadata_revision_id,
        state='current',
        created_at=now,
    )
    record.publication_state = 'current'
    record.processing_state = 'complete'
    record.updated_at = now
    session.add(publication)
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source_id,
            kind='publication_created',
            state='current',
            reason=None,
            details_json=json.dumps({'publication_id': publication.id}, sort_keys=True),
            created_at=now,
        )
    )
    session.flush()
    return publication


def record_metadata_layers(
    session: Session,
    library_record_id: str,
    source_id: str,
    original: dict[str, str],
    analyzed: dict[str, str],
    final: dict[str, str],
    now: datetime,
) -> tuple[LibraryMetadataRevisionRecord, ...]:
    """Persist immutable original, analyzed, and final metadata layers."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    revisions: list[LibraryMetadataRevisionRecord] = []
    for layer, tags in (('original', original), ('analyzed', analyzed), ('final', final)):
        next_revision = (
            max(
                (item.revision for item in record.metadata_revisions if item.layer == layer),
                default=0,
            )
            + 1
        )
        revision = LibraryMetadataRevisionRecord(
            library_record_id=record.id,
            source_id=source_id,
            layer=layer,
            revision=next_revision,
            tags_json=json.dumps(tags, ensure_ascii=False, sort_keys=True),
            actor='worker',
            created_at=now,
        )
        revisions.append(revision)
        session.add(revision)
    record.metadata_state = 'final'
    record.updated_at = now
    session.flush()
    return tuple(revisions)


def append_metadata_revision(
    session: Session,
    library_record_id: str,
    source_id: str,
    layer: str,
    tags: dict[str, str],
    actor: str,
    now: datetime,
) -> LibraryMetadataRevisionRecord:
    """Append one immutable metadata revision for a source layer."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id).with_for_update())
    if record is None:
        raise LookupError(library_record_id)
    if layer == 'original':
        existing = session.scalar(
            select(LibraryMetadataRevisionRecord)
            .where(LibraryMetadataRevisionRecord.library_record_id == record.id)
            .where(LibraryMetadataRevisionRecord.layer == layer)
            .order_by(LibraryMetadataRevisionRecord.revision)
            .limit(1)
        )
        if existing is not None:
            return existing
    latest_revision = session.scalar(
        select(func.max(LibraryMetadataRevisionRecord.revision))
        .where(LibraryMetadataRevisionRecord.library_record_id == record.id)
        .where(LibraryMetadataRevisionRecord.layer == layer)
    )
    revision = LibraryMetadataRevisionRecord(
        library_record_id=record.id,
        source_id=source_id,
        layer=layer,
        revision=(latest_revision or 0) + 1,
        tags_json=json.dumps(tags, ensure_ascii=False, sort_keys=True),
        actor=actor,
        created_at=now,
    )
    session.add(revision)
    record.updated_at = now
    if layer == 'final':
        record.metadata_state = 'final'
    session.flush()
    return revision


def record_event(
    session: Session,
    library_record_id: str,
    kind: str,
    state: str,
    reason: str | None,
    now: datetime,
    source_id: str | None = None,
) -> None:
    """Append a state explanation to a stable library record."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    record.processing_state = state
    record.updated_at = now
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source_id,
            kind=kind,
            state=state,
            reason=reason,
            details_json='{}',
            created_at=now,
        )
    )
    session.flush()


def persist_effective_source_decision(
    session: Session,
    library_record_id: str,
    candidates: tuple[QualityCandidate, ...],
    now: datetime,
    *,
    manual_source_id: str | None = None,
) -> QualityDecision:
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    source_ids = {source.id for source in record.sources}
    if not {candidate.source_id for candidate in candidates}.issubset(source_ids):
        raise ValueError('quality candidates must belong to the library record')
    stored = record.effective_source_decision
    previous = _stored_effective_source_decision(stored)
    decision = evaluate(candidates, previous=previous, manual_source_id=manual_source_id)
    if stored is None:
        stored = EffectiveSourceDecisionRecord(library_record_id=record.id)
        record.effective_source_decision = stored
        session.add(stored)
    stored.source_id = decision.source_id
    stored.baseline_source_id = decision.baseline_source_id
    stored.policy_version = decision.policy_version
    stored.quality_tuple_json = json.dumps(decision.quality_tuple or (), separators=(',', ':'))
    stored.reason = json.dumps(
        {'candidate_set_key': decision.candidate_set_key, 'code': decision.reason.value},
        separators=(',', ':'),
        sort_keys=True,
    )
    stored.updated_at = now
    record.updated_at = now
    if decision.reason.value == 'policy_version_review':
        session.add(
            LibraryEventRecord(
                library_record_id=record.id,
                source_id=decision.source_id,
                kind='quality_policy_review_needed',
                state='needs_review',
                reason='effective source policy version changed',
                details_json=stored.reason,
                created_at=now,
            )
        )
    session.flush()
    return decision


def reevaluate_effective_source_decision(
    session: Session, library_record_id: str, now: datetime, manual_source_id: str | None = None
) -> QualityDecision:
    record = library_record_detail(session, library_record_id)
    confirmed_source_ids = frozenset(
        source.id
        for source in record.sources
        if source.library_record_id == record.id
        and (
            record.musicbrainz_recording_id is not None
            or any(decision.state == 'confirmed' for decision in source.review_decisions)
        )
    )
    candidates = tuple(
        QualityCandidate(
            source_id=source.id,
            codec=source.media_codec or '',
            bit_depth=source.media_bit_depth,
            sample_rate=source.media_sample_rate,
            channels=source.media_channels,
            bitrate=source.media_bitrate,
            confirmed=source.id in confirmed_source_ids,
            intake_state=source.intake_state,
            disappeared=source.disappeared_at is not None,
        )
        for source in record.sources
    )
    return persist_effective_source_decision(session, record.id, candidates, now, manual_source_id=manual_source_id)


def _stored_effective_source_decision(record: EffectiveSourceDecisionRecord | None) -> ExistingDecision | None:
    if record is None:
        return None
    parsed_reason = _json_value(record.reason)
    parsed_values = _json_value(record.quality_tuple_json)
    if not isinstance(parsed_reason, dict):
        raise ValueError('stored effective source decision reason is malformed')
    reason = cast(dict[str, object], parsed_reason)
    candidate_set_key = reason.get('candidate_set_key')
    code = reason.get('code')
    if not isinstance(candidate_set_key, str) or not isinstance(code, str):
        raise ValueError('stored effective source decision reason is malformed')
    if parsed_values == [] and record.source_id is None:
        quality_tuple = None
    elif isinstance(parsed_values, list):
        raw_values = cast(list[object], parsed_values)
        if len(raw_values) != 7 or not all(type(value) is int for value in raw_values):
            raise ValueError('stored effective source decision tuple is malformed')
        values = cast(list[int], raw_values)
        quality_tuple: QualityTuple | None = cast(QualityTuple, tuple(values))
    else:
        raise ValueError('stored effective source decision tuple is malformed')
    return ExistingDecision(
        source_id=record.source_id,
        baseline_source_id=record.baseline_source_id,
        policy_version=record.policy_version,
        quality_tuple=quality_tuple,
        reason=DecisionReason(code),
        candidate_set_key=candidate_set_key,
    )


def _json_value(raw: str) -> object:
    return cast(object, json.loads(raw))


def library_record_detail(session: Session, library_record_id: str) -> LibraryRecord:
    """Load one stable record with its source and publication history."""
    consolidation = session.get(LibraryRecordConsolidationRecord, library_record_id)
    if consolidation is not None:
        library_record_id = consolidation.canonical_library_record_id
    record = session.scalar(
        select(LibraryRecord)
        .where(LibraryRecord.id == library_record_id)
        .options(
            selectinload(LibraryRecord.sources).selectinload(SourceRecord.review_decisions),
            selectinload(LibraryRecord.publications),
            selectinload(LibraryRecord.metadata_revisions),
            selectinload(LibraryRecord.events),
            selectinload(LibraryRecord.effective_source_decision),
        )
    )
    if record is None:
        raise LookupError(library_record_id)
    return record


def library_records(session: Session) -> list[LibraryRecord]:
    """Load stable records for the source/publication catalog."""
    return list(
        session.scalars(
            select(LibraryRecord)
            .where(~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)))
            .options(
                selectinload(LibraryRecord.sources),
                selectinload(LibraryRecord.publications),
            )
        ).all()
    )
