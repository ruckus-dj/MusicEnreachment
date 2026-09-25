from __future__ import annotations

import os
from datetime import datetime
from hashlib import sha256
from pathlib import Path
from typing import final, override

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models.entities import StorageConfigRecord
from music_ingest.models.library import (
    LibraryEventRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
)
from music_ingest.services.publication.locks import acquire_storage_lock

_PROTECTED_ATTEMPT_STATES = ('reserved', 'staged', 'prepared', 'exposed')


@final
class PublicationWithdrawalError(Exception):
    def __init__(self, detail: str) -> None:
        super().__init__(detail)
        self.detail = detail

    @override
    def __str__(self) -> str:
        return self.detail


def withdraw_current_publication(
    session: Session, library_record_id: str, media_root: Path | None, now: datetime
) -> bool:
    acquire_storage_lock(session)
    root = _media_root(session, media_root)
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id).with_for_update())
    if record is None:
        raise LookupError(library_record_id)
    pending = session.scalar(
        select(PublicationAttemptRecord.id)
        .where(PublicationAttemptRecord.library_record_id == library_record_id)
        .where(PublicationAttemptRecord.state.in_(_PROTECTED_ATTEMPT_STATES))
        .limit(1)
    )
    if pending is not None:
        raise PublicationWithdrawalError('publication is being updated; retry after processing completes')
    current = session.scalars(
        select(LibraryPublicationRecord)
        .where(LibraryPublicationRecord.library_record_id == library_record_id)
        .where(LibraryPublicationRecord.state == 'current')
        .with_for_update()
    ).all()
    for publication in current:
        _validate_managed_audio(publication, root)
    if not current:
        record.publication_state = 'absent'
        return False
    current_ids = {publication.id for publication in current}
    source_id = current[0].source_id
    for publication in current:
        publication.state = 'withdrawn'
    record.publication_state = 'absent'
    record.updated_at = now
    if record.lyrics_publication_id in current_ids:
        record.lyrics_status = 'none'
        record.lyrics_path = None
        record.lyrics_publication_id = None
        record.lyrics_sha256 = None
        record.lyrics_updated_at = now
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source_id,
            kind='publication_withdrawn',
            state='absent',
            reason='removed by operator',
            details_json='{}',
            created_at=now,
        )
    )
    session.flush()
    return True


def cleanup_withdrawn_publications(session: Session, library_record_id: str, media_root: Path | None) -> None:
    acquire_storage_lock(session)
    root = _media_root(session, media_root)
    current_paths = frozenset(
        _absolute(Path(path))
        for path in session.scalars(
            select(LibraryPublicationRecord.path).where(LibraryPublicationRecord.state == 'current')
        )
    )
    protected_paths = frozenset(
        _absolute(Path(item.target_directory) / item.target_audio_name)
        for item in session.scalars(
            select(PublicationAttemptRecord)
            .where(PublicationAttemptRecord.cleaned_at.is_(None))
            .where(PublicationAttemptRecord.state.in_(_PROTECTED_ATTEMPT_STATES))
        )
    )
    withdrawn = session.scalars(
        select(LibraryPublicationRecord)
        .where(LibraryPublicationRecord.library_record_id == library_record_id)
        .where(LibraryPublicationRecord.state == 'withdrawn')
    ).all()
    for publication in withdrawn:
        path = _absolute(Path(publication.path))
        if path in current_paths or path in protected_paths:
            continue
        _validate_managed_audio(publication, root)
        if path.exists():
            path.unlink()
            _fsync_directory(path.parent)
        _prune_empty_directories(path.parent, root)


def _media_root(session: Session, fallback: Path | None) -> Path:
    storage = session.get(StorageConfigRecord, 1)
    if storage is not None and storage.state != 'ready':
        raise PublicationWithdrawalError('output migration is active; retry after completion')
    configured = Path(storage.output_root) if storage is not None else fallback
    if configured is None:
        raise PublicationWithdrawalError('managed output is not configured')
    if configured.is_symlink():
        raise PublicationWithdrawalError('managed output root cannot be a symbolic link')
    root = _absolute(configured)
    if not root.is_dir():
        raise PublicationWithdrawalError('managed output root is unavailable')
    return root


def _validate_managed_audio(publication: LibraryPublicationRecord, media_root: Path) -> None:
    path = _absolute(Path(publication.path))
    if (
        not path.is_relative_to(media_root)
        or _has_symlink_component(path, media_root)
        or path.suffix.casefold() == '.nfo'
    ):
        raise PublicationWithdrawalError('publication path is outside managed output')
    if not path.exists():
        return
    if not path.is_file() or _sha256(path) != publication.content_sha256:
        raise PublicationWithdrawalError('published file changed; refusing unsafe removal')


def cleanup_attempt(attempt: PublicationAttemptRecord, session: Session | None = None) -> bool:
    _cleanup_workspace(attempt)
    if attempt.state != 'finalized' or session is None:
        return True
    storage = session.get(StorageConfigRecord, 1)
    if storage is None:
        return True
    configured_root = Path(storage.output_root)
    if configured_root.is_symlink():
        raise ValueError('storage output root must not be a symbolic link')
    media_root = _absolute(configured_root)
    current_paths = frozenset(
        _absolute(Path(path))
        for path in session.scalars(
            select(LibraryPublicationRecord.path).where(LibraryPublicationRecord.state == 'current')
        )
    )
    protected_paths = frozenset(
        _absolute(Path(item.target_directory) / item.target_audio_name)
        for item in session.scalars(
            select(PublicationAttemptRecord)
            .where(PublicationAttemptRecord.id != attempt.id)
            .where(PublicationAttemptRecord.cleaned_at.is_(None))
            .where(PublicationAttemptRecord.state.in_(_PROTECTED_ATTEMPT_STATES))
        )
    )
    superseded = session.scalars(
        select(LibraryPublicationRecord)
        .where(LibraryPublicationRecord.library_record_id == attempt.library_record_id)
        .where(LibraryPublicationRecord.state == 'superseded')
    ).all()
    completed = True
    for publication in superseded:
        path = _absolute(Path(publication.path))
        if not path.is_relative_to(media_root) or _has_symlink_component(path, media_root):
            continue
        if path in protected_paths:
            completed = False
            continue
        if path in current_paths or path.suffix.casefold() == '.nfo':
            continue
        if path.exists():
            if not path.is_file() or _sha256(path) != publication.content_sha256:
                continue
            path.unlink()
            _fsync_directory(path.parent)
        _prune_empty_directories(path.parent, media_root)
    return completed


def _cleanup_workspace(attempt: PublicationAttemptRecord) -> None:
    for directory in (Path(attempt.staging_directory), Path(attempt.backup_directory)):
        if not directory.exists():
            continue
        for name in (attempt.target_audio_name, 'manifest.json', attempt.target_audio_name + '.restore'):
            path = directory / name
            if path.suffix.casefold() != '.nfo':
                path.unlink(missing_ok=True)
        _fsync_directory(directory)
        if not any(directory.iterdir()):
            directory.rmdir()
            _fsync_directory(directory.parent)
    workspace = Path(attempt.staging_directory).parent
    if workspace.parent.name == '.music-ingest-publications' and workspace.exists() and not any(workspace.iterdir()):
        workspace.rmdir()
        _fsync_directory(workspace.parent)


def _prune_empty_directories(directory: Path, media_root: Path) -> None:
    current = directory
    while current != media_root and current.is_relative_to(media_root):
        if current.is_symlink() or not current.is_dir() or any(current.iterdir()):
            return
        current.rmdir()
        parent = current.parent
        _fsync_directory(parent)
        current = parent


def _absolute(path: Path) -> Path:
    return Path(os.path.abspath(path))


def _has_symlink_component(path: Path, media_root: Path) -> bool:
    current = media_root
    for component in path.relative_to(media_root).parts:
        current /= component
        if current.is_symlink():
            return True
    return False


def _sha256(path: Path) -> str:
    digest = sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
