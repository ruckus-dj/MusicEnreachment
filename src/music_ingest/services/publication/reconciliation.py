from __future__ import annotations

import json
import os
import stat
from datetime import datetime
from pathlib import Path

from pydantic import TypeAdapter, ValidationError
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.contracts.reconciliation import PublicationReconciliationResult
from music_ingest.models import (
    LibraryEventRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReleaseArtworkRecord,
    StorageConfigRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.lyrics.reuse import LyricEvidence

_PROTECTED_ATTEMPT_STATES = ('reserved', 'staged', 'prepared', 'exposed')
_LYRIC_EVIDENCE_ADAPTER = TypeAdapter(LyricEvidence)


def reconcile_publication_directory(session: Session, now: datetime) -> PublicationReconciliationResult:
    storage = session.get(StorageConfigRecord, 1)
    if storage is None or storage.state != 'ready':
        raise ValueError('publication storage is not ready')
    configured_root = Path(storage.output_root)
    if not configured_root.is_dir() or configured_root.is_symlink():
        raise ValueError('publication storage must be an existing non-symlink directory')
    media_root = _absolute(configured_root)
    root_device = media_root.stat().st_dev

    publications = tuple(session.scalars(select(LibraryPublicationRecord)).all())
    records = tuple(session.scalars(select(LibraryRecord)).all())
    attempts = tuple(
        session.scalars(select(PublicationAttemptRecord).where(PublicationAttemptRecord.cleaned_at.is_(None))).all()
    )
    publication_files = {_absolute(Path(item.path)) for item in publications}
    active_target_files = {
        _absolute(Path(attempt.target_directory) / attempt.target_audio_name)
        for attempt in attempts
        if attempt.state in _PROTECTED_ATTEMPT_STATES
    }
    track_directories = {path.parent for path in publication_files if _is_available(str(path), media_root)}
    track_directories.update(path.parent for path in active_target_files)
    ancillary_files = {
        _absolute(Path(path))
        for path in session.scalars(select(ReleaseArtworkRecord.path).where(ReleaseArtworkRecord.path.is_not(None)))
        if path is not None
    }
    ancillary_files.update(_lyrics_paths(records))
    represented_files = publication_files | active_target_files
    represented_files.update(path for path in ancillary_files if path.parent in track_directories)
    protected_directories = {
        _absolute(Path(path)) for attempt in attempts for path in (attempt.staging_directory, attempt.backup_directory)
    }
    removed_files, removed_directories, preserved_nfo, unsafe_entries = _remove_orphans(
        media_root,
        root_device,
        represented_files,
        protected_directories,
    )
    missing_publications = 0
    queued_jobs = 0
    already_queued = 0
    deferred_publications = 0
    pending_record_ids = {
        attempt.library_record_id for attempt in attempts if attempt.state in _PROTECTED_ATTEMPT_STATES
    }
    for publication in publications:
        if publication.state != 'current' or _is_available(publication.path, media_root):
            continue
        if publication.library_record_id in pending_record_ids:
            deferred_publications += 1
            continue
        record = session.get(LibraryRecord, publication.library_record_id)
        if record is None:
            continue
        publication.state = 'inactive'
        record.publication_state = 'failed'
        record.updated_at = now
        session.add(
            LibraryEventRecord(
                library_record_id=record.id,
                source_id=publication.source_id,
                kind='publication_file_missing',
                state='failed',
                reason='current publication file is unavailable',
                details_json=json.dumps({'publication_id': publication.id, 'path': publication.path}),
                created_at=now,
            )
        )
        if JobRepository(session).enqueue_selection_refresh(record.id, now) is None:
            already_queued += 1
        else:
            queued_jobs += 1
        missing_publications += 1

    return PublicationReconciliationResult(
        removed_files=removed_files,
        removed_directories=removed_directories,
        preserved_nfo=preserved_nfo,
        missing_publications=missing_publications,
        queued_jobs=queued_jobs,
        already_queued=already_queued,
        deferred_publications=deferred_publications,
        unsafe_entries=unsafe_entries,
    )


def _lyrics_paths(records: tuple[LibraryRecord, ...]) -> set[Path]:
    paths = {_absolute(Path(record.lyrics_path)) for record in records if record.lyrics_path}
    for record in records:
        if not record.lyrics_evidence_json:
            continue
        try:
            evidence = _LYRIC_EVIDENCE_ADAPTER.validate_json(record.lyrics_evidence_json)
        except ValidationError:
            continue
        if evidence.path:
            paths.add(_absolute(Path(evidence.path)))
    return paths


def _remove_orphans(
    media_root: Path,
    root_device: int,
    represented_files: set[Path],
    protected_directories: set[Path],
) -> tuple[int, int, int, int]:
    removed_files = 0
    removed_directories = 0
    preserved_nfo = 0
    unsafe_entries = 0
    directories: list[Path] = []
    for current_text, directory_names, file_names in os.walk(media_root, topdown=True, followlinks=False):
        current = Path(current_text)
        directories.append(current)
        retained_directories: list[str] = []
        for name in directory_names:
            path = current / name
            if _is_protected_subtree(path, protected_directories):
                continue
            metadata = path.lstat()
            if stat.S_ISLNK(metadata.st_mode):
                if path not in represented_files:
                    path.unlink()
                    _fsync_directory(current)
                    removed_files += 1
                continue
            if metadata.st_dev != root_device:
                unsafe_entries += 1
                continue
            retained_directories.append(name)
        directory_names[:] = retained_directories
        if _is_protected_subtree(current, protected_directories):
            directory_names.clear()
            continue
        for name in file_names:
            path = current / name
            if path.suffix.casefold() == '.nfo':
                preserved_nfo += 1
                continue
            if path in represented_files:
                continue
            metadata = path.lstat()
            if metadata.st_dev != root_device or not (stat.S_ISREG(metadata.st_mode) or stat.S_ISLNK(metadata.st_mode)):
                unsafe_entries += 1
                continue
            path.unlink()
            _fsync_directory(current)
            removed_files += 1
    for directory in reversed(directories):
        if directory == media_root or _is_protected_subtree(directory, protected_directories):
            continue
        try:
            directory.rmdir()
        except OSError:
            continue
        _fsync_directory(directory.parent)
        removed_directories += 1
    return removed_files, removed_directories, preserved_nfo, unsafe_entries


def _is_available(path_text: str, media_root: Path) -> bool:
    path = _absolute(Path(path_text))
    if not path.is_relative_to(media_root):
        return False
    current = media_root
    for component in path.relative_to(media_root).parts:
        current /= component
        if current.is_symlink():
            return False
    try:
        return stat.S_ISREG(path.lstat().st_mode)
    except FileNotFoundError:
        return False


def _is_protected_subtree(path: Path, protected_directories: set[Path]) -> bool:
    absolute = _absolute(path)
    return any(absolute == protected or absolute.is_relative_to(protected) for protected in protected_directories)


def _absolute(path: Path) -> Path:
    return Path(os.path.abspath(path))


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
