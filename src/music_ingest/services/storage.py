from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import final, override

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models import (
    LibraryPublicationRecord,
    PublicationAttemptRecord,
    ReleaseArtworkRecord,
    SourceRootRecord,
    StorageConfigRecord,
)
from music_ingest.services.publication.attempts import _sha256
from music_ingest.services.publication.locks import acquire_migration_lock, acquire_storage_lock
from music_ingest.services.storage_migration import MigrationJournal, build_migration


@final
class StorageValidationError(Exception):
    def __init__(self, detail: str) -> None:
        super().__init__(detail)
        self.detail = detail

    @override
    def __str__(self) -> str:
        return self.detail


@dataclass(frozen=True, slots=True)
class StorageBrowser:
    path: Path
    parent_path: Path | None
    items: tuple[Path, ...]


@dataclass(frozen=True, slots=True)
class StorageOutputPreview:
    output_root: Path
    same_filesystem: bool
    file_count: int


@final
class StorageService:
    def __init__(self, session: Session, browse_roots: tuple[Path, ...], default_output: Path) -> None:
        self._session = session
        self._browse_roots = tuple(self._canonical_directory(path) for path in browse_roots)
        self._default_output = self._canonical_directory(default_output)

    def config(self) -> StorageConfigRecord:
        record = self._session.get(StorageConfigRecord, 1)
        if record is not None:
            return record
        now = datetime.now(UTC)
        record = StorageConfigRecord(
            id=1,
            output_root=str(self._default_output),
            state='ready',
            generation=1,
            updated_at=now,
        )
        self._session.add(record)
        self._session.flush()
        return record

    def browse(self, raw_path: str | None) -> StorageBrowser:
        path = self._browse_roots[0] if raw_path is None else self._canonical_browse_path(raw_path)
        parent = path.parent if path not in self._browse_roots else None
        items = tuple(
            sorted(
                (child for child in path.iterdir() if not child.is_symlink() and child.is_dir()),
                key=lambda item: item.name.casefold(),
            )
        )
        return StorageBrowser(path, parent, items)

    def validate_input(self, raw_path: str) -> Path:
        acquire_storage_lock(self._session)
        candidate = self._canonical_browse_path(raw_path)
        config = self.config()
        if config.state == 'migrating' and config.migration_json is not None:
            journal = MigrationJournal.model_validate_json(config.migration_json)
            if self._overlaps(candidate, Path(journal.destination)):
                raise StorageValidationError('input root must not overlap the pending output root')
        output = self._canonical_directory(Path(self.config().output_root))
        if self._overlaps(candidate, output):
            raise StorageValidationError('input root must not overlap the output root')
        for root in self._session.scalars(select(SourceRootRecord)).all():
            if root.id != 'historical-unmanaged' and self._overlaps(candidate, Path(root.canonical_path)):
                raise StorageValidationError('input roots must not overlap')
        return candidate

    def preview_output(self, raw_path: str) -> StorageOutputPreview:
        output = self._canonical_browse_path(raw_path)
        self._validate_output(output)
        old = self._canonical_directory(Path(self.config().output_root))
        return StorageOutputPreview(
            output,
            old.stat().st_dev == output.stat().st_dev,
            sum(1 for item in old.rglob('*') if item.is_file()),
        )

    def move_output(self, raw_path: str) -> StorageConfigRecord:
        """Persist a relocation request; the processing worker resumes its manifest."""
        acquire_migration_lock(self._session, exclusive=True)
        acquire_storage_lock(self._session)
        config = self.config()
        self._session.refresh(config)
        destination = self._canonical_browse_path(raw_path)
        if config.state == 'migrating':
            journal = MigrationJournal.model_validate_json(config.migration_json or '{}')
            if destination != Path(journal.destination):
                raise StorageValidationError('another output migration is already active')
            return config
        preview = self.preview_output(raw_path)
        old = self._canonical_directory(Path(config.output_root))
        if old == destination:
            return config
        if self._overlaps(old, destination):
            raise StorageValidationError('old and new output roots must not overlap')
        if any(destination.iterdir()):
            raise StorageValidationError('new output root must be empty')
        active_attempt = self._session.scalar(
            select(PublicationAttemptRecord).where(PublicationAttemptRecord.cleaned_at.is_(None))
        )
        if active_attempt is not None:
            raise StorageValidationError('output root cannot move while publication recovery is active')
        managed = {row.path for row in self._session.scalars(select(LibraryPublicationRecord)).all()}
        managed.update(row.path for row in self._session.scalars(select(ReleaseArtworkRecord)).all() if row.path)
        try:
            for publication in self._session.scalars(
                select(LibraryPublicationRecord).where(LibraryPublicationRecord.state == 'current')
            ).all():
                path = Path(publication.path)
                if path.is_relative_to(old) and _sha256(path) != publication.content_sha256:
                    raise ValueError(f'current publication hash mismatch: {path}')
            journal = build_migration(old, preview.output_root, managed)
        except (OSError, ValueError) as error:
            raise StorageValidationError(str(error)) from error
        config.migration_json = journal.model_dump_json()
        config.state = 'migrating'
        config.updated_at = datetime.now(UTC)
        self._session.flush()
        return config

    def _validate_output(self, output: Path) -> None:
        for root in self._session.scalars(select(SourceRootRecord)).all():
            if root.id != 'historical-unmanaged' and self._overlaps(output, Path(root.canonical_path)):
                raise StorageValidationError('output root must not overlap an input root')

    def _canonical_browse_path(self, raw_path: str) -> Path:
        candidate = self._canonical_directory(Path(raw_path))
        if not any(candidate.is_relative_to(root) for root in self._browse_roots):
            raise StorageValidationError('path is outside the container storage mounts')
        return candidate

    @staticmethod
    def _canonical_directory(path: Path) -> Path:
        if path.is_symlink():
            raise StorageValidationError('storage path must not be a symbolic link')
        try:
            canonical = path.resolve(strict=True)
        except FileNotFoundError as error:
            raise StorageValidationError('storage path must be an existing directory') from error
        if not canonical.is_dir():
            raise StorageValidationError('storage path must be an existing directory')
        return canonical

    @staticmethod
    def _overlaps(left: Path, right: Path) -> bool:
        return left == right or left.is_relative_to(right) or right.is_relative_to(left)
