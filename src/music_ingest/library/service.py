from __future__ import annotations

import json
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Literal, cast

from sqlalchemy import exists, func, or_, select
from sqlalchemy.orm import Session, selectinload
from sqlalchemy.sql.elements import ColumnElement

from music_ingest.models import SourceRecord, SourceTagRecord
from music_ingest.models.library import (
    EffectiveSourceDecisionRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
    ReleaseArtworkRecord,
)
from music_ingest.quality_policy import (
    DecisionReason,
    ExistingDecision,
    QualityCandidate,
    QualityDecision,
    QualityTuple,
    evaluate,
)


@dataclass(frozen=True, slots=True)
class CatalogArtist:
    name: str
    track_count: int


@dataclass(frozen=True, slots=True)
class CatalogAlbum:
    album_id: str | None
    album_name: str
    track_count: int
    artwork_url: str | None


@dataclass(frozen=True, slots=True)
class CatalogTrack:
    record_id: str
    source_id: str
    release_id: str | None
    publication_state: str
    artist_name: str
    album_name: str
    title: str
    track_number: str | None


@dataclass(frozen=True, slots=True)
class CatalogSourceTags:
    record_id: str
    source_id: str
    release_id: str | None
    publication_state: str
    tags: dict[str, str]


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
                selectinload(LibraryRecord.metadata_revisions),
            )
        ).all()
    )


def _manual_action_predicate(action: Literal['analysis-error', 'needs-review']) -> ColumnElement[bool]:
    non_replaced_source = exists(
        select(SourceRecord.id).where(
            SourceRecord.library_record_id == LibraryRecord.id,
            SourceRecord.intake_state != 'replaced',
        )
    )
    if action == 'analysis-error':
        return non_replaced_source & or_(
            LibraryRecord.processing_state.in_(['retrying', 'blocked_infrastructure', 'quarantined']),
            LibraryRecord.publication_state == 'failed',
            exists(
                select(SourceRecord.id).where(
                    SourceRecord.library_record_id == LibraryRecord.id,
                    SourceRecord.intake_state == 'invalid_audio',
                )
            ),
        )
    return non_replaced_source & or_(
        LibraryRecord.processing_state == 'needs_review',
        LibraryRecord.match_state == 'needs_review',
    )


def library_manual_action_records(
    session: Session, action: Literal['analysis-error', 'needs-review']
) -> list[LibraryRecord]:
    """Load only active library records matching one manual-action category."""
    return list(
        session.scalars(
            select(LibraryRecord)
            .where(
                ~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)),
                _manual_action_predicate(action),
            )
            .options(
                selectinload(LibraryRecord.sources),
                selectinload(LibraryRecord.publications),
                selectinload(LibraryRecord.metadata_revisions),
            )
        ).all()
    )


def library_manual_action_counts(session: Session) -> tuple[int, int]:
    """Count active records in both manual-action categories without loading their rows."""
    base_query = select(func.count(func.distinct(LibraryRecord.id))).where(
        ~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id))
    )
    analysis_errors = session.scalar(base_query.where(_manual_action_predicate('analysis-error'))) or 0
    needs_review = session.scalar(base_query.where(_manual_action_predicate('needs-review'))) or 0
    return analysis_errors, needs_review


def _catalog_artists(tags: dict[str, str]) -> tuple[str, ...]:
    value = tags.get('ALBUMARTIST') or tags.get('ARTIST') or ''
    return tuple(dict.fromkeys(name.strip() for name in value.split(';') if name.strip()))


def _catalog_source_tags(session: Session, published: bool | None) -> list[CatalogSourceTags]:
    """Load tags for present source files without hydrating library relationships."""
    revision_query = (
        select(
            LibraryRecord.id,
            LibraryRecord.musicbrainz_release_id,
            LibraryRecord.publication_state,
            LibraryMetadataRevisionRecord.source_id,
            LibraryMetadataRevisionRecord.layer,
            LibraryMetadataRevisionRecord.tags_json,
        )
        .join(
            LibraryMetadataRevisionRecord,
            LibraryMetadataRevisionRecord.library_record_id == LibraryRecord.id,
        )
        .join(
            SourceRecord,
            (SourceRecord.id == LibraryMetadataRevisionRecord.source_id)
            & (SourceRecord.library_record_id == LibraryRecord.id),
        )
    )
    revision_query = revision_query.where(
        LibraryMetadataRevisionRecord.layer.in_(['final', 'original']),
        SourceRecord.intake_state == 'present',
        ~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)),
    )
    if published is not None:
        revision_query = revision_query.where(
            LibraryRecord.publication_state == 'current' if published else LibraryRecord.publication_state != 'current'
        )
    tags_by_source: dict[tuple[str, str], tuple[str, dict[str, str], str | None, str]] = {}
    for record_id, release_id, publication_state, source_id, layer, tags_json in session.execute(revision_query):
        key = (record_id, source_id)
        current = tags_by_source.get(key)
        if current is None or (current[0] == 'original' and layer == 'final'):
            tags_by_source[key] = (layer, json.loads(tags_json), release_id, publication_state)

    raw_query = (
        select(
            SourceRecord.library_record_id,
            SourceRecord.id,
            LibraryRecord.musicbrainz_release_id,
            LibraryRecord.publication_state,
            SourceTagRecord.tag_name,
            SourceTagRecord.value,
        )
        .join(SourceTagRecord, SourceTagRecord.source_id == SourceRecord.id)
        .join(LibraryRecord, LibraryRecord.id == SourceRecord.library_record_id)
        .where(
            SourceRecord.intake_state == 'present',
            ~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)),
        )
    )
    if published is not None:
        raw_query = raw_query.where(
            LibraryRecord.publication_state == 'current' if published else LibraryRecord.publication_state != 'current'
        )
    raw_by_source: dict[tuple[str, str], tuple[str, str | None, str, dict[str, str]]] = {}
    for record_id, source_id, release_id, publication_state, tag_name, value in session.execute(raw_query):
        raw_by_source.setdefault((record_id, source_id), (record_id, release_id, publication_state, {}))[3][
            tag_name
        ] = value
    result: list[CatalogSourceTags] = []
    for key, (record_id, release_id, publication_state, tags) in raw_by_source.items():
        revision = tags_by_source.get(key)
        result.append(
            CatalogSourceTags(
                record_id,
                key[1],
                release_id,
                revision[3] if revision else publication_state,
                revision[1] if revision else tags,
            )
        )
    return result


def library_artist_names(session: Session, published: bool | None = None) -> list[CatalogArtist]:
    """Load distinct Final/Original album artists and their present source counts."""
    counts: dict[str, set[str]] = {}
    for source in _catalog_source_tags(session, published):
        for artist in _catalog_artists(source.tags):
            counts.setdefault(artist, set()).add(source.record_id)
    return [CatalogArtist(name, len(record_ids)) for name, record_ids in sorted(counts.items())]


def library_active_record_count(session: Session, published: bool | None = None) -> int:
    """Count active library records represented by a source or current publication."""
    query = (
        select(func.count(func.distinct(LibraryRecord.id)))
        .outerjoin(
            SourceRecord,
            (SourceRecord.library_record_id == LibraryRecord.id) & (SourceRecord.intake_state == 'present'),
        )
        .outerjoin(
            LibraryPublicationRecord,
            (LibraryPublicationRecord.library_record_id == LibraryRecord.id)
            & (LibraryPublicationRecord.state == 'current'),
        )
        .where(
            or_(SourceRecord.id.is_not(None), LibraryPublicationRecord.id.is_not(None)),
            ~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)),
        )
    )
    if published is not None:
        query = query.where(
            LibraryRecord.publication_state == 'current' if published else LibraryRecord.publication_state != 'current'
        )
    return int(session.scalar(query) or 0)


def library_artist_albums(
    session: Session,
    artist_name: str,
    *,
    published: bool | None = None,
) -> list[CatalogAlbum]:
    """Load distinct Final/Original albums for one exact artist name."""
    groups: dict[tuple[str | None, str], set[str]] = {}
    for source in _catalog_source_tags(session, published):
        if artist_name not in _catalog_artists(source.tags) or not source.tags.get('ALBUM'):
            continue
        groups.setdefault((source.release_id, source.tags['ALBUM']), set()).add(source.record_id)
    release_ids = {album_id for album_id, _ in groups if album_id is not None}
    artwork_urls = {
        release_mbid: f'/api/library/release-artwork/{release_mbid}'
        for release_mbid in session.scalars(
            select(ReleaseArtworkRecord.release_mbid).where(
                ReleaseArtworkRecord.release_mbid.in_(release_ids),
                ReleaseArtworkRecord.state == 'ready',
                ReleaseArtworkRecord.path.is_not(None),
            )
        )
    }
    return [
        CatalogAlbum(
            album_id,
            album_name,
            len(record_ids),
            artwork_urls.get(album_id) if album_id is not None else None,
        )
        for (album_id, album_name), record_ids in sorted(
            groups.items(), key=lambda item: (item[0][1], item[0][0] or '')
        )
    ]


def library_album_tracks(
    session: Session,
    artist_name: str,
    *,
    album_id: str | None = None,
    album_name: str | None = None,
    published: bool | None = None,
) -> list[CatalogTrack]:
    """Load minimal Final/Original track data for one exact artist and album."""
    tracks: list[CatalogTrack] = []
    for source in _catalog_source_tags(session, published):
        album_value = source.tags.get('ALBUM', '')
        if artist_name not in _catalog_artists(source.tags):
            continue
        if album_id is not None and source.release_id != album_id:
            continue
        if album_name is not None and (source.release_id is not None or album_value != album_name):
            continue
        tracks.append(
            CatalogTrack(
                record_id=source.record_id,
                source_id=source.source_id,
                release_id=source.release_id,
                publication_state=source.publication_state,
                artist_name=artist_name,
                album_name=album_value,
                title=source.tags.get('TITLE', ''),
                track_number=source.tags.get('TRACKNUMBER'),
            )
        )
    return tracks
