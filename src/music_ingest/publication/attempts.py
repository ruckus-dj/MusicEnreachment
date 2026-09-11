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

from music_ingest.models.jobs import JobRepository
from music_ingest.models.library import (
    LibraryEventRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
)
from music_ingest.publication.locks import acquire_storage_lock

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
    completion_state: str = 'complete'


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
        completion_state=request.completion_state,
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
    manifest = _manifest(attempt)
    manifest_path = Path(attempt.staging_directory) / 'manifest.json'
    manifest_path.write_text(manifest, encoding='utf-8')
    _fsync_file(manifest_path)
    attempt.manifest_sha256 = sha256(manifest.encode()).hexdigest()
    _fsync_file(output)
    _fsync_directory(output.parent)
    _event(session, attempt, 'publication_attempt_staged', 'publishing', now)
    session.flush()


def expose_attempt(session: Session, attempt: PublicationAttemptRecord, now: datetime) -> None:
    staging = Path(attempt.staging_directory)
    target = Path(attempt.target_directory) / attempt.target_audio_name
    backup = Path(attempt.backup_directory) / attempt.target_audio_name
    output = staging / attempt.target_audio_name
    if attempt.state not in {'staged', 'prepared'} or attempt.output_sha256 != _sha256(output):
        raise ValueError('publication attempt is not a valid staged output')
    manifest = _manifest(attempt)
    manifest_path = staging / 'manifest.json'
    _ = manifest_path.write_text(manifest, encoding='utf-8')
    _fsync_file(manifest_path)
    _fsync_directory(staging)
    target.parent.mkdir(parents=True, exist_ok=True)
    backup.parent.mkdir(parents=True, exist_ok=True)
    if backup.exists() and target.exists():
        raise ValueError('publication attempt backup and target both exist')
    if target.exists():
        _ = os.replace(target, backup)
        _fsync_directory(backup.parent)
        _fsync_directory(target.parent)
    try:
        _ = os.replace(output, target)
    except OSError:
        if backup.exists() and not target.exists():
            os.replace(backup, target)
        raise
    _fsync_directory(target.parent)
    _fsync_directory(backup.parent)
    _fsync_directory(staging)
    attempt.manifest_sha256 = sha256(manifest.encode()).hexdigest()
    attempt.state = 'exposed'
    attempt.exposed_at = now
    _event(session, attempt, 'publication_attempt_exposed', 'publishing', now)
    session.flush()


def finalize_attempt(
    session: Session, attempt: PublicationAttemptRecord, now: datetime, *, lrclib_enabled: bool = True
) -> LibraryPublicationRecord:
    """Finalize one exposed publication and, when the provider is enabled, queue its lyric fetch.

    ``lrclib_enabled`` mirrors the persisted provider switch of the live process: a disabled provider must not be
    handed new work, so the record is materialized as "no lyrics were requested" instead of pending, and no job is
    queued for lyrics that will not be fetched. Queued jobs from before the switch was turned off are settled by the
    handler itself.
    """
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
    # Recovery may discover a rename that happened before the crashed process fsynced it.
    _fsync_file(target)
    _fsync_directory(target.parent)
    _fsync_directory(manifest_path.parent)
    backup_directory = Path(attempt.backup_directory)
    if backup_directory.is_dir():
        _fsync_directory(backup_directory)
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
    record.processing_state = attempt.completion_state
    record.updated_at = now
    # The new publication supersedes any sidecar written for the previous one: lyric state is materialized as
    # pending with no bound path, publication, or hash until a fetch validates lyrics against this output. A
    # disabled provider never fetches, so its records are materialized as "no lyrics were requested" instead.
    record.lyrics_status = 'pending' if lrclib_enabled else 'none'
    record.lyrics_path = None
    record.lyrics_publication_id = None
    record.lyrics_sha256 = None
    record.lyrics_updated_at = now
    attempt.state = 'finalized'
    attempt.finalized_at = now
    _event(session, attempt, 'publication_attempt_finalized', 'complete', now)
    # Same transaction as the current publication: a coalesced fetch never blocks or rolls back publication. A
    # disabled provider is never handed new work, so no fetch is queued for it at all.
    if lrclib_enabled:
        _ = JobRepository(session).enqueue_lrclib_fetch(attempt.library_record_id, now)
    session.flush()
    return publication


def finalize_and_cleanup_attempt(
    session: Session, attempt: PublicationAttemptRecord, now: datetime, *, lrclib_enabled: bool = True
) -> LibraryPublicationRecord:
    publication = finalize_attempt(session, attempt, now, lrclib_enabled=lrclib_enabled)
    session.commit()
    acquire_storage_lock(session)
    cleanup_attempt(attempt)
    attempt.cleaned_at = now
    session.commit()
    return publication


def cleanup_attempt(attempt: PublicationAttemptRecord) -> None:
    # Delete only files owned by this attempt. Sidecars (especially .nfo) are never removed.
    for directory in (Path(attempt.staging_directory), Path(attempt.backup_directory)):
        if not directory.exists():
            continue
        for name in (attempt.target_audio_name, 'manifest.json', attempt.target_audio_name + '.restore'):
            path = directory / name
            if path.suffix.lower() != '.nfo':
                path.unlink(missing_ok=True)
        _fsync_directory(directory)
        if not any(directory.iterdir()):
            directory.rmdir()
            _fsync_directory(directory.parent)
    workspace = Path(attempt.staging_directory).parent
    if workspace.parent.name == '.music-ingest-publications' and workspace.exists() and not any(workspace.iterdir()):
        workspace.rmdir()
        _fsync_directory(workspace.parent)


def reconcile_attempts(session: Session, now: datetime, *, lrclib_enabled: bool = True) -> None:
    """Run only outside a handler savepoint; every filesystem change has a durable intent.

    ``lrclib_enabled`` is the live persisted provider switch, so recovery neither queues a fetch nor promises
    lyrics while the operator has the provider turned off.
    """
    acquire_storage_lock(session)
    attempt_ids = session.scalars(
        select(PublicationAttemptRecord.id)
        .where(PublicationAttemptRecord.cleaned_at.is_(None))
        .order_by(PublicationAttemptRecord.created_at, PublicationAttemptRecord.id)
        .limit(100)
    ).all()
    for attempt_id in attempt_ids:
        acquire_storage_lock(session)
        attempt = session.get(PublicationAttemptRecord, attempt_id, populate_existing=True)
        if attempt is None or attempt.cleaned_at is not None:
            continue
        if attempt.state in {'finalized', 'failed'}:
            cleanup_attempt(attempt)
            attempt.cleaned_at = now
            session.commit()
            continue
        if attempt.state == 'prepared':
            # A previous process may have completed the rename without committing exposed.
            if _exposed_output_is_recoverable(attempt):
                attempt.state = 'exposed'
            else:
                target = Path(attempt.target_directory) / attempt.target_audio_name
                owner = session.scalar(
                    select(LibraryPublicationRecord)
                    .where(LibraryPublicationRecord.path == str(target.resolve()))
                    .where(LibraryPublicationRecord.state == 'current')
                )
                if owner is not None and owner.library_record_id != attempt.library_record_id:
                    attempt.state = 'failed'
                    attempt.failure_reason = 'destination belongs to another library record'
                    session.commit()
                    continue
                expose_attempt(session, attempt, now)
            session.commit()
            acquire_storage_lock(session)
        if attempt.state == 'staged' and _exposed_output_is_recoverable(attempt):
            attempt.state = 'exposed'
        if attempt.state in {'reserved', 'staged'}:
            _restore_backup(attempt)
            attempt.state = 'failed'
            attempt.failure_reason = 'worker restart before output exposure'
            _event(session, attempt, 'publication_attempt_recovered_failed', 'retrying', now)
            session.commit()
            acquire_storage_lock(session)
            cleanup_attempt(attempt)
            attempt.cleaned_at = now
            session.commit()
            continue
        try:
            finalize_attempt(session, attempt, now, lrclib_enabled=lrclib_enabled)
        except OSError, ValueError:
            _restore_backup(attempt)
            attempt.state = 'failed'
            attempt.failure_reason = 'exposed output failed manifest or hash recovery verification'
            _event(session, attempt, 'publication_attempt_recovered_failed', 'retrying', now)
        # Do not catch commit errors: leave the durable journal and backup for restart.
        session.commit()
        acquire_storage_lock(session)
        cleanup_attempt(attempt)
        attempt.cleaned_at = now
        session.commit()
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
    restored = backup.with_name(backup.name + '.restore')
    shutil.copy2(backup, restored)
    _fsync_file(restored)
    _ = os.replace(restored, target)
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
