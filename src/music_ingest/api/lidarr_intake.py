from __future__ import annotations

import hashlib
from datetime import UTC, datetime
from pathlib import Path
from typing import Annotated, Literal, assert_never

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, ValidationError
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.persistence.models import (
    JobRecord,
    ReleaseFileRecord,
    TombstoneRecord,
    TrackRecord,
    WebhookReceiptRecord,
)
from music_ingest.persistence.repository import WebhookReceiptInput, WebhookReceiptRepository


class LidarrTrackFile(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    path: Path


class LidarrRenamedTrackFile(LidarrTrackFile):
    previous_path: Path = Field(alias='previousPath')


class LidarrAlbum(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    id: int | str


class LidarrTestPayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Test'] = Field(alias='eventType')


class LidarrDownloadPayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Download'] = Field(alias='eventType')
    track_files: tuple[LidarrTrackFile, ...] = Field(alias='trackFiles', min_length=1)
    is_upgrade: bool = Field(alias='isUpgrade')
    deleted_files: tuple[LidarrTrackFile, ...] = Field(default=(), alias='deletedFiles')


class LidarrRenamePayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Rename'] = Field(alias='eventType')
    renamed_track_files: tuple[LidarrRenamedTrackFile, ...] = Field(alias='renamedTrackFiles', min_length=1)


class LidarrAlbumDeletePayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['AlbumDelete'] = Field(alias='eventType')
    album: LidarrAlbum
    deleted_files: bool = Field(alias='deletedFiles')


type LidarrEvent = Annotated[
    LidarrTestPayload | LidarrDownloadPayload | LidarrRenamePayload | LidarrAlbumDeletePayload,
    Field(discriminator='event_type'),
]

_EVENT_ADAPTER: TypeAdapter[LidarrEvent] = TypeAdapter(LidarrEvent)


class LidarrIntakeError(ValueError):
    pass


class LidarrDispatchResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    job_id: str | None
    replayed: bool


def parse_lidarr_event(raw_payload: bytes) -> LidarrEvent:
    try:
        return _EVENT_ADAPTER.validate_json(raw_payload)
    except ValidationError as error:
        raise LidarrIntakeError('Lidarr payload is malformed or unsupported') from error


def dispatch_lidarr_event(
    session: Session, event: LidarrEvent, raw_payload: bytes, incoming_root: Path, provenance_root: Path
) -> LidarrDispatchResult:
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
            _validate_download_paths(event, incoming_root)
            source_ids = _intake_download_sources(session, event, provenance_root)
            return _record_job(session, event, fingerprint, payload_json, received_at, source_ids)
        case LidarrRenamePayload():
            _validate_rename_paths(event, incoming_root)
            result = _record_job(session, event, fingerprint, payload_json, received_at)
            _record_renames(session, event)
            return result
        case LidarrAlbumDeletePayload():
            result = _record_job(session, event, fingerprint, payload_json, received_at)
            _record_album_tombstones(session, event, received_at)
            return result
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


def _intake_download_sources(session: Session, event: LidarrDownloadPayload, provenance_root: Path) -> tuple[str, ...]:
    return tuple(
        intake_source(
            session,
            IntakeRequest(
                source_path=track_file.path.resolve(strict=True),
                origin=Origin.LIDARR,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
            provenance_root,
        ).source_id
        for track_file in event.track_files
    )


def _receipt_exists(session: Session, fingerprint: str) -> bool:
    return (
        session.scalar(select(WebhookReceiptRecord.id).where(WebhookReceiptRecord.event_fingerprint == fingerprint))
        is not None
    )


def _validate_download_paths(event: LidarrDownloadPayload, incoming_root: Path) -> None:
    root = incoming_root.resolve(strict=True)
    for track_file in event.track_files:
        try:
            source_path = track_file.path.resolve(strict=True)
        except FileNotFoundError as error:
            raise LidarrIntakeError('Download trackFiles[].path must name an existing incoming file') from error
        if not source_path.is_file() or not source_path.is_relative_to(root):
            raise LidarrIntakeError('Download trackFiles[].path must name a file below the incoming root')


def _validate_rename_paths(event: LidarrRenamePayload, incoming_root: Path) -> None:
    root = incoming_root.resolve(strict=True)
    for track_file in event.renamed_track_files:
        if not track_file.previous_path.is_absolute() or not track_file.path.is_absolute():
            raise LidarrIntakeError('Rename paths must be absolute')
        previous_path = track_file.previous_path.resolve(strict=False)
        destination_path = track_file.path.resolve(strict=False)
        if not previous_path.is_relative_to(root) or not destination_path.is_relative_to(root):
            raise LidarrIntakeError('Rename paths must remain below the incoming root')


def _record_renames(session: Session, event: LidarrRenamePayload) -> None:
    for track_file in event.renamed_track_files:
        release_file = session.scalar(
            select(ReleaseFileRecord).where(ReleaseFileRecord.relative_path == str(track_file.previous_path))
        )
        if release_file is not None:
            release_file.relative_path = str(track_file.path)


def _record_album_tombstones(session: Session, event: LidarrAlbumDeletePayload, received_at: datetime) -> None:
    if not event.deleted_files:
        return
    release_files = session.scalars(
        select(ReleaseFileRecord).join(ReleaseFileRecord.track).where(TrackRecord.release_id == str(event.album.id))
    ).all()
    for release_file in release_files:
        if release_file.tombstone is None:
            session.add(
                TombstoneRecord(release_file=release_file, reason='lidarr_album_delete', recorded_at=received_at)
            )
