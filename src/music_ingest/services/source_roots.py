from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path
from typing import final, override
from uuid import uuid4

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models import SourceRootRecord


@final
class SourceRootValidationError(Exception):
    def __init__(self, detail: str) -> None:
        super().__init__(detail)
        self.detail = detail

    @override
    def __str__(self) -> str:
        return self.detail


@final
class SourceRootConflictError(Exception):
    def __init__(self, canonical_path: str) -> None:
        super().__init__(canonical_path)
        self.canonical_path = canonical_path

    @override
    def __str__(self) -> str:
        return f'source root is already configured: {self.canonical_path}'


@final
class SourceRootService:
    def __init__(self, session: Session, parent: Path | None = None) -> None:
        self._session = session
        self._parent = None if parent is None else self._resolve_parent(parent)

    def list(self) -> tuple[SourceRootRecord, ...]:
        return tuple(
            self._session.scalars(
                select(SourceRootRecord)
                .where(SourceRootRecord.id != 'historical-unmanaged')
                .order_by(SourceRootRecord.created_at)
            ).all()
        )

    def candidates(self) -> tuple[Path, ...]:
        if self._parent is None:
            return ()
        return tuple(
            sorted(
                (
                    child.resolve(strict=True)
                    for child in self._parent.iterdir()
                    if not child.is_symlink() and child.is_dir()
                ),
                key=lambda path: path.name.casefold(),
            )
        )

    def create(self, path: str, display_name: str) -> SourceRootRecord:
        canonical_path = self._canonical_immediate_child(Path(path))
        duplicate = self._session.scalar(
            select(SourceRootRecord).where(SourceRootRecord.canonical_path == canonical_path)
        )
        if duplicate is not None:
            raise SourceRootConflictError(canonical_path)
        now = datetime.now(UTC)
        root = SourceRootRecord(
            id=f'root-{uuid4().hex}',
            display_name=display_name,
            canonical_path=canonical_path,
            enabled=True,
            scan_state='never_scanned',
            created_at=now,
            updated_at=now,
        )
        self._session.add(root)
        self._session.flush()
        return root

    def update(self, root_id: str, display_name: str, enabled: bool) -> SourceRootRecord | None:
        root = self._session.get(SourceRootRecord, root_id)
        if root is None:
            return None
        _ = self._canonical_immediate_child(Path(root.canonical_path))
        root.display_name = display_name
        root.enabled = enabled
        root.updated_at = datetime.now(UTC)
        self._session.flush()
        return root

    def remove(self, root_id: str) -> bool:
        root = self._session.get(SourceRootRecord, root_id)
        if root is None:
            return False
        if root.id == 'historical-unmanaged':
            return False
        historical = self._session.get(SourceRootRecord, 'historical-unmanaged')
        if historical is None:
            now = datetime.now(UTC)
            historical = SourceRootRecord(
                id='historical-unmanaged',
                display_name='historical-unmanaged',
                canonical_path='historical-unmanaged://',
                enabled=False,
                scan_state='never_scanned',
                created_at=now,
                updated_at=now,
            )
            self._session.add(historical)
        for source in tuple(root.sources):
            source.source_root = historical
        self._session.delete(root)
        self._session.flush()
        return True

    @staticmethod
    def _resolve_parent(parent: Path) -> Path:
        if parent.is_symlink():
            raise SourceRootValidationError('source roots parent must not be a symlink')
        try:
            canonical_parent = parent.resolve(strict=True)
        except FileNotFoundError as error:
            raise SourceRootValidationError('source roots parent must be an existing directory') from error
        if not canonical_parent.is_dir():
            raise SourceRootValidationError('source roots parent must be an existing directory')
        return canonical_parent

    def _canonical_immediate_child(self, candidate: Path) -> str:
        if candidate.is_symlink():
            raise SourceRootValidationError('source root must not be a symlink')
        try:
            canonical_path = candidate.resolve(strict=True)
        except FileNotFoundError as error:
            raise SourceRootValidationError('source root must be an existing directory') from error
        if not canonical_path.is_dir():
            raise SourceRootValidationError('source root must be an existing directory')
        if self._parent is not None and canonical_path.parent != self._parent:
            raise SourceRootValidationError('source root must be a canonical immediate child of the mounted parent')
        return str(canonical_path)
