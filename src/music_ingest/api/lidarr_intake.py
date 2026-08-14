from __future__ import annotations

import hashlib
from datetime import UTC, datetime
from pathlib import Path
from typing import assert_never

from pydantic import TypeAdapter, ValidationError
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.dto import (
    LidarrAlbumDeletePayload,
    LidarrDispatchResult,
    LidarrDownloadPayload,
    LidarrEvent,
    LidarrIntakeError,
    LidarrRenamePayload,
    LidarrTestPayload,
)
from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.library.service import record_event
from music_ingest.models import JobRecord, SourceRecord, SourceRootRecord, WebhookReceiptRecord
from music_ingest.models.repositories import WebhookReceiptInput, WebhookReceiptRepository

_EVENT_ADAPTER: TypeAdapter[LidarrEvent] = TypeAdapter(LidarrEvent)


def parse_lidarr_event(raw_payload: bytes) -> LidarrEvent:
    try:
        return _EVENT_ADAPTER.validate_json(raw_payload)
    except ValidationError as error:
        raise LidarrIntakeError('Lidarr payload is malformed or unsupported') from error


def dispatch_lidarr_event(session: Session, event: LidarrEvent, raw_payload: bytes) -> LidarrDispatchResult:
    fingerprint = hashlib.sha256(raw_payload).hexdigest()
    payload_json = raw_payload.decode('utf-8')
    received_at = datetime.now(UTC)
    match event:
        case LidarrTestPayload():
            replayed = _receipt_exists(session, fingerprint)
            receipt = WebhookReceiptRepository(session).record_or_reuse(
                WebhookReceiptInput(fingerprint, 'lidarr', payload_json, received_at)
            )
            _ = receipt
            return LidarrDispatchResult(job_id=None, replayed=replayed)
        case LidarrDownloadPayload():
            source_ids = _intake_download_sources(session, event)
            return _record_job(session, event, fingerprint, payload_json, received_at, source_ids)
        case LidarrRenamePayload():
            _record_renames(session, event)
            return _record_control_receipt(session, fingerprint, payload_json, received_at)
        case LidarrAlbumDeletePayload():
            return _record_control_receipt(session, fingerprint, payload_json, received_at)
        case unreachable:
            assert_never(unreachable)


def _record_job(
    session: Session,
    event: LidarrDownloadPayload | LidarrRenamePayload | LidarrAlbumDeletePayload,
    fingerprint: str,
    payload_json: str,
    received_at: datetime,
    source_ids: tuple[str, ...] = (),
) -> LidarrDispatchResult:
    job_id = f'lidarr-{fingerprint}'
    existing_job = session.get(JobRecord, job_id)
    if existing_job is None:
        try:
            with session.begin_nested():
                for index, source_id in enumerate(source_ids or (None,)):
                    session.add(
                        JobRecord(
                            id=job_id if index == 0 else f'{job_id}-{index + 1}',
                            source_id=source_id,
                            kind=f'lidarr_{event.event_type.lower()}',
                            state='queued',
                            created_at=received_at,
                        )
                    )
                session.flush()
        except IntegrityError:
            existing_job = session.get(JobRecord, job_id)
            if existing_job is None:
                raise
    receipt = WebhookReceiptRepository(session).record_or_reuse(
        WebhookReceiptInput(fingerprint, 'lidarr', payload_json, received_at, job_id)
    )
    _ = receipt
    return LidarrDispatchResult(job_id=job_id, replayed=existing_job is not None)


def _record_control_receipt(
    session: Session, fingerprint: str, payload_json: str, received_at: datetime
) -> LidarrDispatchResult:
    replayed = _receipt_exists(session, fingerprint)
    receipt = WebhookReceiptRepository(session).record_or_reuse(
        WebhookReceiptInput(fingerprint, 'lidarr', payload_json, received_at)
    )
    _ = receipt
    return LidarrDispatchResult(job_id=None, replayed=replayed)


def _intake_download_sources(session: Session, event: LidarrDownloadPayload) -> tuple[str, ...]:
    return tuple(
        intake_source(
            session,
            IntakeRequest(
                source_path=_source_path(track_file.path),
                source_root_id=_source_root_id(session, track_file.path),
                origin=Origin.LIDARR,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        ).source_id
        for track_file in event.track_files
    )


def _receipt_exists(session: Session, fingerprint: str) -> bool:
    return (
        session.scalar(select(WebhookReceiptRecord.id).where(WebhookReceiptRecord.event_fingerprint == fingerprint))
        is not None
    )


def _record_renames(session: Session, event: LidarrRenamePayload) -> None:
    for track_file in event.renamed_track_files:
        source = session.scalar(select(SourceRecord).where(SourceRecord.source_path == str(track_file.previous_path)))
        if source is not None:
            destination_root_id = _source_root_id(session, track_file.path, require_file=False)
            if destination_root_id != source.source_root_id:
                raise LidarrIntakeError('Rename paths must remain within the configured source root')
            source.source_path = str(track_file.path)
            if source.library_record is not None:
                record_event(
                    session,
                    source.library_record.id,
                    'source_moved',
                    source.library_record.processing_state,
                    'source_renamed_by_provider',
                    datetime.now(UTC),
                    source.id,
                )


def _source_path(path: Path) -> Path:
    if path.is_symlink():
        raise LidarrIntakeError('Download trackFiles[].path cannot be a symbolic link')
    try:
        source_path = path.resolve(strict=True)
    except FileNotFoundError as error:
        raise LidarrIntakeError('Download trackFiles[].path must name an existing source file') from error
    if not source_path.is_file():
        raise LidarrIntakeError('Download trackFiles[].path must name an existing source file')
    return source_path


def _source_root_id(session: Session, path: Path, *, require_file: bool = True) -> str:
    if not path.is_absolute():
        raise LidarrIntakeError('Source paths must be absolute')
    source_path = _source_path(path) if require_file else path.resolve(strict=False)
    root_ids = tuple(
        root.id
        for root in session.scalars(select(SourceRootRecord).where(SourceRootRecord.enabled.is_(True)))
        if source_path.is_relative_to(Path(root.canonical_path))
    )
    if len(root_ids) != 1:
        raise LidarrIntakeError('Source path must be below exactly one configured source root')
    return root_ids[0]
