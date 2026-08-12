from __future__ import annotations

import os
import shutil
from dataclasses import dataclass
from datetime import UTC, datetime
from errno import EXDEV
from pathlib import Path
from typing import final, override

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models import (
    LibraryPublicationRecord,
    PublicationAttemptRecord,
    SourceRootRecord,
    StorageConfigRecord,
)


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
        candidate = self._canonical_browse_path(raw_path)
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
        preview = self.preview_output(raw_path)
        config = self.config()
        old = self._canonical_directory(Path(config.output_root))
        if old == preview.output_root:
            return config
        if any(preview.output_root.iterdir()):
            raise StorageValidationError('new output root must be empty')
        active_attempt = self._session.scalar(
            select(PublicationAttemptRecord).where(PublicationAttemptRecord.state.not_in(['finalized', 'failed']))
        )
        if active_attempt is not None:
            raise StorageValidationError('output root cannot move while publication recovery is active')
        config.state = 'migrating'
        self._session.flush()
        for child in tuple(old.iterdir()):
            destination = preview.output_root / child.name
            if preview.same_filesystem:
                try:
                    os.replace(child, destination)
                except OSError as error:
                    if error.errno != EXDEV:
                        raise
                    self._copy_and_remove(child, destination)
            else:
                self._copy_and_remove(child, destination)
        for publication in self._session.scalars(select(LibraryPublicationRecord)).all():
            path = Path(publication.path)
            if path.is_relative_to(old):
                publication.path = str(preview.output_root / path.relative_to(old))
        config.output_root = str(preview.output_root)
        config.state = 'ready'
        config.generation += 1
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

    @staticmethod
    def _copy_and_remove(source: Path, destination: Path) -> None:
        if source.is_dir():
            _ = shutil.copytree(source, destination, symlinks=False)
            _ = shutil.rmtree(source)
        else:
            _ = shutil.copy2(source, destination, follow_symlinks=False)
            source.unlink()
