"""Strict, explicit source transformations. This module never opens media files."""

import json
from datetime import datetime
from pathlib import Path

from sqlalchemy import false, or_, select
from sqlalchemy.exc import OperationalError
from sqlalchemy.orm import Session

from music_ingest.contracts.source_encoding import (
    EncodingApplied,
    EncodingChoice,
    EncodingDetail,
    EncodingField,
    EncodingPreview,
    EncodingPreviewField,
    EncodingRequest,
)
from music_ingest.models import JobRecord, LibraryEventRecord, ProviderCandidateRunRecord, SourceRecord, SourceTagRecord
from music_ingest.models.library import LibraryRecord, PublicationAttemptRecord
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import append_metadata_revision, ensure_source_record
from music_ingest.services.normalize.encoding_suggestions import suggest_encodings
from music_ingest.services.normalize.source_values import source_values


class EncodingConflict(ValueError):
    pass


class EncodingInvalid(ValueError):
    def __init__(self, preview: EncodingPreview) -> None:
        self.preview = preview
        super().__init__('invalid field choices')


def encoding_detail(source: SourceRecord) -> EncodingDetail:
    detail = EncodingDetail(
        source_id=source.id,
        source_revision=source.source_metadata_revision,
        fields=[
            EncodingField(
                field_id=item.id,
                tag_name=item.tag_name,
                container=item.format_name,
                physical_id=item.physical_id,
                extraction_version=item.extraction_version,
                selected=item.selected,
                original_value=item.original_value if item.original_value is not None else item.value,
                current_value=item.value,
                raw_evidence_available=item.prepared_bytes is not None,
                declared_codec=item.declared_codec,
                decision_origin=item.decision_origin,
                applied_choice=EncodingChoice.model_validate_json(item.applied_choice_json)
                if item.applied_choice_json
                else None,
            )
            for item in source.tag_observations
        ],
    )
    detail.suggestions = suggest_encodings(detail.fields)
    return detail


def preview_encoding(source: SourceRecord, request: EncodingRequest) -> EncodingPreview:
    if source.source_metadata_revision != request.expected_revision:
        raise EncodingConflict('source_revision_changed')
    fields = {item.id: item for item in source.tag_observations}
    results = [
        preview_field(fields[choice.field_id], choice)
        if choice.field_id in fields
        else EncodingPreviewField(field_id=choice.field_id, value=None, status='error', error='unknown_field')
        for choice in request.choices
    ]
    return EncodingPreview(
        source_id=source.id,
        source_revision=source.source_metadata_revision,
        valid=all(item.error is None for item in results),
        fields=results,
    )


def apply_encoding(session: Session, source: SourceRecord, request: EncodingRequest, now: datetime) -> EncodingApplied:
    # Savepoint maps every NOWAIT failure and rolls back any partial apply changes.
    try:
        with session.begin_nested():
            return _apply_encoding(session, source, request, now)
    except OperationalError as error:
        raise EncodingConflict('source_processing_busy') from error


def _apply_encoding(session: Session, source: SourceRecord, request: EncodingRequest, now: datetime) -> EncodingApplied:
    active = _lock_encoding(session, source)
    preview = preview_encoding(source, request)
    if not preview.valid:
        raise EncodingInvalid(preview)
    return _write_encoding(session, source, request, preview, now, active)


def _lock_encoding(session: Session, source: SourceRecord) -> tuple[JobRecord, ...]:
    session.refresh(source, with_for_update={'nowait': True})
    session.expire(source, ['tag_observations'])
    # Reservation/finalization/recovery serialize on this same canonical record.
    # NOWAIT avoids inversion with publication code that already owns that lock.
    if source.library_record_id:
        session.scalar(
            select(LibraryRecord).where(LibraryRecord.id == source.library_record_id).with_for_update(nowait=True)
        )
    pending = session.scalar(
        select(PublicationAttemptRecord.id)
        .where(
            or_(
                PublicationAttemptRecord.source_id == source.id,
                PublicationAttemptRecord.library_record_id == source.library_record_id
                if source.library_record_id
                else false(),
            ),
            PublicationAttemptRecord.state.in_(['reserved', 'staged', 'prepared', 'exposed']),
        )
        .limit(1)
    )
    if pending is not None:
        raise EncodingConflict('source_processing_busy')
    active = tuple(
        session.scalars(
            select(JobRecord)
            .where(
                or_(
                    JobRecord.source_id == source.id,
                    JobRecord.library_record_id == source.library_record_id if source.library_record_id else false(),
                    JobRecord.folder_path == str(Path(source.source_path).parent),
                ),
                JobRecord.state.in_(['queued', 'running']),
            )
            .with_for_update(nowait=True)
        ).all()
    )
    if any(job.state == 'running' or job.kind == 'filesystem_scan' for job in active):
        raise EncodingConflict('source_processing_busy')
    return active


def _write_encoding(
    session: Session,
    source: SourceRecord,
    request: EncodingRequest,
    preview: EncodingPreview,
    now: datetime,
    active: tuple[JobRecord, ...] = (),
    *,
    origin: str = 'manual',
    enqueue: bool = True,
) -> EncodingApplied:
    text_changed = any(item.status == 'changed' for item in preview.fields)
    fields = {item.id: item for item in source.tag_observations}
    decision_changed = any(
        choice.mode != 'keep'
        and (
            fields[choice.field_id].applied_choice_json != choice.model_dump_json()
            or fields[choice.field_id].decision_origin != origin
        )
        for choice in request.choices
    )
    if not text_changed and not decision_changed:
        return EncodingApplied(**preview.model_dump(), queued=False)
    record = ensure_source_record(session, source, now)
    changes: list[dict[str, object]] = []
    for result, choice in zip(preview.fields, request.choices, strict=True):
        field = fields[result.field_id]
        if choice.mode == 'keep':
            continue
        changes.append(
            {'field_id': field.id, 'before': field.value, 'after': result.value, 'choice': choice.model_dump()}
        )
        if field.original_value is None:
            field.original_value = field.value
        if result.value is not None:
            field.value = result.value
        field.applied_choice_json = choice.model_dump_json()
        field.decision_origin = origin
    source.source_metadata_revision += 1
    append_metadata_revision(
        session,
        record.id,
        source.id,
        'original',
        source_values(source.tag_observations),
        'source_encoding',
        now,
        revise_original=True,
    )
    if text_changed and enqueue:
        for job in active:
            if job.kind != 'folder_release_selection':
                job.state = 'superseded'
        # Retire automatic candidates without destroying their provenance.
        source.candidate_runs.append(
            ProviderCandidateRunRecord(
                provider_name='musicbrainz', source_metadata_revision=source.source_metadata_revision, created_at=now
            )
        )
    else:
        # Effective input is identical: carry live work/evidence to the decision revision.
        # Do not resurrect already obsolete generations or other sources' folder work.
        for job in active:
            if job.source_id == source.id and job.source_metadata_revision == request.expected_revision:
                job.source_metadata_revision = source.source_metadata_revision
        for run in source.candidate_runs:
            if run.source_metadata_revision == request.expected_revision:
                run.source_metadata_revision = source.source_metadata_revision
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source.id,
            kind='source_encoding_applied',
            state='analyzing' if text_changed else record.processing_state,
            reason=f'{origin} per-field source recovery',
            details_json=json.dumps(
                {
                    'decision_origin': origin,
                    'before_revision': request.expected_revision,
                    'source_revision': source.source_metadata_revision,
                    'changes': changes,
                },
                ensure_ascii=False,
            ),
            created_at=now,
        )
    )
    if text_changed:
        record.processing_state = 'analyzing'
    session.flush()
    queued = (
        JobRepository(session).enqueue(source.id, 'musicbrainz_analysis', now) if text_changed and enqueue else None
    )
    return EncodingApplied(
        source_id=source.id,
        source_revision=source.source_metadata_revision,
        valid=True,
        fields=preview.fields,
        queued=queued is not None,
    )


def backfill_source_encoding(
    session: Session,
    source: SourceRecord,
    now: datetime,
    *,
    apply: bool = False,
) -> dict[str, object]:
    """DB-only recovery, serialized like manual Apply; caller owns the transaction."""
    report: dict[str, object] = {'source_id': source.id, 'status': 'unchanged', 'manual_fields': 0, 'changes': []}
    try:
        with session.begin_nested():
            active = _lock_encoding(session, source)
            fields = source.tag_observations
            report['manual_fields'] = sum(bool(f.applied_choice_json) and f.decision_origin != 'auto' for f in fields)
            report['source_revision'] = source.source_metadata_revision
            if not fields or any(
                f.original_value is None or f.extraction_version is None for f in fields if f.selected
            ):
                report['status'] = 'legacy_missing'
                return report
            request = automatic_request(source)
            if request is None:
                report['review_fields'] = sum(s.state == 'review' for s in encoding_detail(source).suggestions)
                return report
            preview = preview_encoding(source, request)
            if not preview.valid:
                report['status'] = 'invalid'
                return report
            report['changes'] = [field.model_dump() for field in preview.fields]
            report['status'] = 'would_change'
            if apply:
                result = _write_encoding(session, source, request, preview, now, active, origin='auto')
                report.update(status='changed', source_revision=result.source_revision, queued=result.queued)
    except OperationalError, EncodingConflict:
        report['status'] = 'busy'
    return report


def automatic_request(source: SourceRecord) -> EncodingRequest | None:
    """Plan only supported, selected, unprotected fields; never mutate on reads."""
    detail = encoding_detail(source)
    choices = [
        suggestion.choice
        for suggestion in detail.suggestions
        if suggestion.state == 'suggested'
        and suggestion.choice is not None
        and next(field.selected for field in detail.fields if field.field_id == suggestion.field_id)
    ]
    return EncodingRequest(expected_revision=source.source_metadata_revision, choices=choices) if choices else None


def recover_import_encoding(session: Session, source: SourceRecord, now: datetime) -> int:
    """Called by the owning initial worker before provider jobs exist, never by GET."""
    request = automatic_request(source)
    if request is None:
        return 0
    preview = preview_encoding(source, request)
    if not preview.valid:
        raise EncodingInvalid(preview)
    _write_encoding(session, source, request, preview, now, origin='auto', enqueue=False)
    return sum(field.status == 'changed' for field in preview.fields)


def _codec_value(field: SourceTagRecord, codec: str) -> str:
    original = field.original_value if field.original_value is not None else field.value
    # A matching declaration does not rule out mojibake stored as valid Unicode.
    # Only mode='original' restores unconditionally; codec choices must recover strictly.
    # Known byte-oriented observations have actual transport-prepared bytes.
    # Unicode containers instead need inverse mojibake recovery, not raw UTF decoding.
    if field.prepared_bytes is not None and field.declared_codec in {'latin-1', 'cp1252', 'cp1251', 'cp866', 'koi8-r'}:
        value = field.prepared_bytes.decode(codec, errors='strict')
        if value.encode(codec, errors='strict') != field.prepared_bytes:
            raise UnicodeError('inverse_roundtrip_failed')
        return value
    candidates: set[str] = set()
    for mistaken in ('latin-1', 'cp1252'):
        try:
            value = original.encode(mistaken, errors='strict').decode(codec, errors='strict')
            if value.encode(codec, errors='strict').decode(mistaken, errors='strict') == original:
                candidates.add(value)
        except UnicodeError:
            continue
    if len(candidates) != 1:
        raise UnicodeError('ambiguous_or_unavailable_recovery')
    return candidates.pop()


def preview_field(field: SourceTagRecord, choice: EncodingChoice) -> EncodingPreviewField:
    value = field.value
    error: str | None = None
    try:
        if choice.mode == 'original':
            value = field.original_value if field.original_value is not None else field.value
        elif choice.mode == 'codec' and choice.decode_codec is not None:
            value = _codec_value(field, choice.decode_codec)
        elif choice.mode == 'decode':
            if field.prepared_bytes is None:
                error = 'raw_evidence_unavailable'
            elif choice.decode_codec is not None:
                value = field.prepared_bytes.decode(choice.decode_codec, errors='strict')
        elif choice.mode == 'unicode' and choice.encode_codec is not None and choice.decode_codec is not None:
            value = field.value.encode(choice.encode_codec, errors='strict').decode(
                choice.decode_codec, errors='strict'
            )
            inverse = value.encode(choice.decode_codec, errors='strict').decode(choice.encode_codec, errors='strict')
            if inverse != field.value:
                error = 'inverse_roundtrip_failed'
        if '\x00' in value:
            error = 'nul_not_allowed'
    except UnicodeError:
        error = 'strict_conversion_failed'
    return EncodingPreviewField(
        field_id=field.id,
        value=None if error else value,
        status='error' if error else ('unchanged' if value == field.value else 'changed'),
        error=error,
    )
