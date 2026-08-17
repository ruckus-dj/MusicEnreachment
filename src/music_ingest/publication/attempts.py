from __future__ import annotations

import os
import shutil
from dataclasses import dataclass
from datetime import datetime
from hashlib import sha256
from pathlib import Path

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models.library import (
    LibraryEventRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
)

_MANIFEST_ADAPTER = TypeAdapter(dict[str, int | str | None])


@dataclass(frozen=True, slots=True)
class PublicationAttemptRequest:
    attempt_id: str
    library_record_id: str
    source_id: str
    metadata_revision_id: int | None
    target_directory: Path
    target_audio_name: str
    staging_directory: Path
    backup_directory: Path
    now: datetime


def reserve_attempt(session: Session, request: PublicationAttemptRequest) -> PublicationAttemptRecord:
    attempt = PublicationAttemptRecord(
        id=request.attempt_id,
        library_record_id=request.library_record_id,
        source_id=request.source_id,
        metadata_revision_id=request.metadata_revision_id,
        state='reserved',
        target_directory=str(request.target_directory),
        target_audio_name=request.target_audio_name,
        staging_directory=str(request.staging_directory),
        backup_directory=str(request.backup_directory),
        created_at=request.now,
    )
    session.add(attempt)
    session.add(
        LibraryEventRecord(
            library_record_id=attempt.library_record_id,
            source_id=attempt.source_id,
            kind='publication_attempt_reserved',
            state='publishing',
            reason=attempt.id,
            details_json='{}',
            created_at=request.now,
        )
    )
    session.flush()
    return attempt


def mark_staged(session: Session, attempt: PublicationAttemptRecord, now: datetime) -> None:
    output = Path(attempt.staging_directory) / attempt.target_audio_name
    attempt.output_sha256 = _sha256(output)
    attempt.state = 'staged'
    _fsync_file(output)
    _fsync_directory(output.parent)
    _event(session, attempt, 'publication_attempt_staged', 'publishing', now)
    session.flush()


def expose_attempt(session: Session, attempt: PublicationAttemptRecord, now: datetime) -> None:
    staging = Path(attempt.staging_directory)
    target = Path(attempt.target_directory) / attempt.target_audio_name
    backup = Path(attempt.backup_directory) / attempt.target_audio_name
    output = staging / attempt.target_audio_name
    if attempt.state != 'staged' or attempt.output_sha256 != _sha256(output):
        raise ValueError('publication attempt is not a valid staged output')
    manifest = _manifest(attempt)
    manifest_path = staging / 'manifest.json'
    _ = manifest_path.write_text(manifest, encoding='utf-8')
    _fsync_file(manifest_path)
    _fsync_directory(staging)
    target.parent.mkdir(parents=True, exist_ok=True)
    backup.parent.mkdir(parents=True, exist_ok=True)
    if backup.exists():
        raise ValueError('publication attempt backup already exists')
    if target.exists():
        _ = os.replace(target, backup)
    try:
        _ = os.replace(output, target)
    except OSError:
        if backup.exists() and not target.exists():
            os.replace(backup, target)
        raise
    _fsync_directory(target.parent)
    attempt.manifest_sha256 = sha256(manifest.encode()).hexdigest()
    attempt.state = 'exposed'
    attempt.exposed_at = now
    _event(session, attempt, 'publication_attempt_exposed', 'publishing', now)
    session.flush()


def finalize_attempt(session: Session, attempt: PublicationAttemptRecord, now: datetime) -> LibraryPublicationRecord:
    if attempt.state == 'finalized':
        publication = session.get(LibraryPublicationRecord, f'publication-{attempt.id}')
        if publication is None:
            raise LookupError(attempt.id)
        return publication
    target = Path(attempt.target_directory) / attempt.target_audio_name
    manifest_path = Path(attempt.staging_directory) / 'manifest.json'
    output = target
    manifest = manifest_path.read_text(encoding='utf-8')
    if attempt.state != 'exposed' or attempt.manifest_sha256 != sha256(manifest.encode()).hexdigest():
        raise ValueError('publication attempt manifest is invalid')
    parsed = _MANIFEST_ADAPTER.validate_json(manifest)
    if parsed != _manifest_values(attempt) or attempt.output_sha256 != _sha256(target):
        raise ValueError('publication attempt output is invalid')
    current = session.scalars(
        select(LibraryPublicationRecord)
        .where(LibraryPublicationRecord.library_record_id == attempt.library_record_id)
        .where(LibraryPublicationRecord.state == 'current')
        .with_for_update()
    ).all()
    for publication in current:
        publication.state = 'superseded'
    record = session.get(LibraryRecord, attempt.library_record_id)
    if record is None:
        raise LookupError(attempt.library_record_id)
    publication = LibraryPublicationRecord(
        id=f'publication-{attempt.id}',
        library_record_id=attempt.library_record_id,
        source_id=attempt.source_id,
        path=str(output),
        format_name=output.suffix.removeprefix('.'),
        content_sha256=attempt.output_sha256,
        metadata_revision_id=attempt.metadata_revision_id,
        state='current',
        created_at=now,
    )
    session.add(publication)
    record.publication_state = 'current'
    record.processing_state = 'complete'
    record.updated_at = now
    attempt.state = 'finalized'
    attempt.finalized_at = now
    _event(session, attempt, 'publication_attempt_finalized', 'complete', now)
    session.flush()
    return publication


def finalize_and_cleanup_attempt(
    session: Session, attempt: PublicationAttemptRecord, now: datetime
) -> LibraryPublicationRecord:
    publication = finalize_attempt(session, attempt, now)
    session.commit()
    cleanup_attempt(attempt)
    return publication


def cleanup_attempt(attempt: PublicationAttemptRecord) -> None:
    shutil.rmtree(attempt.staging_directory, ignore_errors=True)
    shutil.rmtree(attempt.backup_directory, ignore_errors=True)


def reconcile_attempts(session: Session, now: datetime) -> None:
    attempts = session.scalars(
        select(PublicationAttemptRecord).where(
            PublicationAttemptRecord.state.in_(('reserved', 'staged', 'exposed', 'finalized'))
        )
    ).all()
    for attempt in attempts:
        if attempt.state == 'finalized':
            cleanup_attempt(attempt)
            continue
        if attempt.state == 'staged' and _exposed_output_is_recoverable(attempt):
            attempt.state = 'exposed'
            _ = finalize_and_cleanup_attempt(session, attempt, now)
            continue
        if attempt.state in {'reserved', 'staged'}:
            if (
                attempt.state == 'staged'
                and (Path(attempt.backup_directory) / attempt.target_audio_name).is_file()
                and not (Path(attempt.target_directory) / attempt.target_audio_name).exists()
            ):
                _restore_backup(attempt)
            cleanup_attempt(attempt)
            attempt.state = 'failed'
            attempt.failure_reason = 'worker restart before output exposure'
            _event(session, attempt, 'publication_attempt_recovered_failed', 'retrying', now)
            continue
        try:
            _ = finalize_and_cleanup_attempt(session, attempt, now)
        except OSError, ValueError:
            _restore_backup(attempt)
            attempt.state = 'failed'
            attempt.failure_reason = 'exposed output failed manifest or hash recovery verification'
            _event(session, attempt, 'publication_attempt_recovered_failed', 'retrying', now)
    session.flush()


def _event(session: Session, attempt: PublicationAttemptRecord, kind: str, state: str, now: datetime) -> None:
    session.add(
        LibraryEventRecord(
            library_record_id=attempt.library_record_id,
            source_id=attempt.source_id,
            kind=kind,
            state=state,
            reason=attempt.id,
            details_json='{}',
            created_at=now,
        )
    )


def _manifest(attempt: PublicationAttemptRecord) -> str:
    return _MANIFEST_ADAPTER.dump_json(_manifest_values(attempt), by_alias=False).decode()


def _manifest_values(attempt: PublicationAttemptRecord) -> dict[str, int | str | None]:
    return {
        'attempt_id': attempt.id,
        'metadata_revision_id': attempt.metadata_revision_id,
        'output_sha256': attempt.output_sha256,
        'source_id': attempt.source_id,
    }


def _sha256(path: Path) -> str:
    digest = sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _fsync_file(path: Path) -> None:
    with path.open('rb') as source:
        os.fsync(source.fileno())


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _restore_backup(attempt: PublicationAttemptRecord) -> None:
    target = Path(attempt.target_directory) / attempt.target_audio_name
    backup = Path(attempt.backup_directory) / attempt.target_audio_name
    if not backup.exists():
        return
    if target.exists():
        target.unlink()
    _ = os.replace(backup, target)
    _fsync_directory(target.parent)


def _exposed_output_is_recoverable(attempt: PublicationAttemptRecord) -> bool:
    target = Path(attempt.target_directory) / attempt.target_audio_name
    manifest_path = Path(attempt.staging_directory) / 'manifest.json'
    if not target.is_file() or not manifest_path.is_file():
        return False
    try:
        manifest = manifest_path.read_text(encoding='utf-8')
        parsed = _MANIFEST_ADAPTER.validate_json(manifest)
    except OSError, ValueError:
        return False
    return (
        parsed == _manifest_values(attempt)
        and attempt.manifest_sha256 == sha256(manifest.encode()).hexdigest()
        and attempt.output_sha256 == _sha256(target)
    )
