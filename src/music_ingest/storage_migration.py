from __future__ import annotations

import os
import shutil
from datetime import UTC, datetime
from pathlib import Path
from typing import Literal
from uuid import uuid4

from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models import LibraryPublicationRecord, ReleaseArtworkRecord, StorageConfigRecord
from music_ingest.publication.attempts import _fsync_directory, _fsync_file, _sha256
from music_ingest.publication.locks import acquire_storage_lock


class MigrationFile(BaseModel):
    relative_path: str
    sha256: str
    managed: bool


class MigrationJournal(BaseModel):
    id: str
    source: str
    destination: str
    files: list[MigrationFile]
    phase: Literal['copying', 'cleanup', 'complete'] = 'copying'
    cursor: int = 0
    failure_reason: str | None = None


def build_migration(source: Path, destination: Path, managed: set[str]) -> MigrationJournal:
    files: list[MigrationFile] = []
    for path in sorted(source.rglob('*')):
        if path.is_symlink():
            raise ValueError(f'output migration refuses symbolic link: {path}')
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError(f'output migration requires regular files: {path}')
        files.append(
            MigrationFile(
                relative_path=str(path.relative_to(source)),
                sha256=_sha256(path),
                managed=str(path) in managed and path.suffix.lower() != '.nfo',
            )
        )
    return MigrationJournal(id=uuid4().hex, source=str(source), destination=str(destination), files=files)


def _safe_path(root: Path, relative: str) -> Path:
    path = root / relative
    if not path.is_relative_to(root) or '..' in Path(relative).parts or Path(relative).is_absolute():
        raise ValueError('invalid storage migration relative path')
    for component in (root, *path.parents, path):
        if component.is_symlink():
            raise ValueError(f'storage migration refuses symbolic link: {component}')
    return path


def _copy_file(journal: MigrationJournal, item: MigrationFile) -> None:
    source = _safe_path(Path(journal.source), item.relative_path)
    destination = _safe_path(Path(journal.destination), item.relative_path)
    if _sha256(source) != item.sha256:
        raise ValueError(f'source changed during storage migration: {source}')
    if destination.exists():
        if _sha256(destination) == item.sha256:
            _sync_copy(destination, Path(journal.destination))
            return
        raise ValueError(f'destination differs from storage migration manifest: {destination}')
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(journal.destination) / f'.music-ingest-move-{journal.id}'
    if temporary.is_symlink():
        raise ValueError('storage migration temporary path is a symbolic link')
    shutil.copyfile(source, temporary)
    _fsync_file(temporary)
    if _sha256(temporary) != item.sha256:
        raise ValueError(f'storage migration copy hash mismatch: {source}')
    os.replace(temporary, destination)
    _sync_copy(destination, Path(journal.destination))


def _sync_copy(destination: Path, root: Path) -> None:
    # Recovery can find a rename performed before the previous process fsynced its directory.
    _fsync_file(destination)
    parent = destination.parent
    while parent.is_relative_to(root):
        _fsync_directory(parent)
        parent = parent.parent


def _remove_managed_copy(journal: MigrationJournal, item: MigrationFile) -> None:
    if not item.managed:
        return
    source = _safe_path(Path(journal.source), item.relative_path)
    destination = _safe_path(Path(journal.destination), item.relative_path)
    if _sha256(destination) != item.sha256:
        raise ValueError(f'relocated output failed cleanup verification: {destination}')
    if source.exists():
        if _sha256(source) != item.sha256:
            raise ValueError(f'old output changed before cleanup: {source}')
        source.unlink()
        _fsync_directory(source.parent)


def resume_storage_migration(session: Session) -> bool:
    """One bounded, committed step. The shared lock excludes publication and other movers."""
    acquire_storage_lock(session)
    config = session.get(StorageConfigRecord, 1, populate_existing=True)
    if config is None or config.state != 'migrating':
        return False
    journal = MigrationJournal.model_validate_json(config.migration_json or '{}')
    try:
        if journal.cursor < len(journal.files):
            item = journal.files[journal.cursor]
            if journal.phase == 'copying':
                _copy_file(journal, item)
            else:
                _remove_managed_copy(journal, item)
            journal.cursor += 1
        elif journal.phase == 'copying':
            # Recheck the entire destination before the atomic database root/path switch.
            for item in journal.files:
                path = _safe_path(Path(journal.destination), item.relative_path)
                if _sha256(path) != item.sha256:
                    raise ValueError(f'storage migration output hash mismatch: {path}')
            old = Path(journal.source)
            for publication in session.scalars(select(LibraryPublicationRecord)).all():
                path = Path(publication.path)
                if path.is_relative_to(old):
                    publication.path = str(Path(journal.destination) / path.relative_to(old))
            for artwork in session.scalars(select(ReleaseArtworkRecord)).all():
                if artwork.path is not None and Path(artwork.path).is_relative_to(old):
                    artwork.path = str(Path(journal.destination) / Path(artwork.path).relative_to(old))
            config.output_root = journal.destination
            config.generation += 1
            journal.phase = 'cleanup'
            journal.cursor = 0
        else:
            journal.phase = 'complete'
            config.state = 'ready'
        journal.failure_reason = None
    except (OSError, ValueError) as error:
        journal.failure_reason = str(error)
        config.migration_json = journal.model_dump_json()
        config.updated_at = datetime.now(UTC)
        session.commit()
        raise
    config.migration_json = journal.model_dump_json()
    config.updated_at = datetime.now(UTC)
    session.commit()
    return True
