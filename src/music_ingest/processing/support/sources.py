from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

from sqlalchemy import select
from sqlalchemy.orm import Session, raiseload, selectinload

from music_ingest.models import (
    ProviderCandidateRunRecord,
    SourceRecord,
)
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing.candidates import (
    _folder_selection_root,
)
from music_ingest.processing.execution import QuarantineSource
from music_ingest.source_boundary import SourceBoundaryError, resolve_owned_source


@dataclass(frozen=True, slots=True)
class SourceAccess:
    session: Session

    def source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self.session.get(SourceRecord, claimed.job.source_id)
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def locked_source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self.session.scalar(
            select(SourceRecord)
            .where(SourceRecord.id == claimed.job.source_id)
            .with_for_update()
            .execution_options(populate_existing=True)
        )
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def candidate_selection_source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self.session.scalar(
            select(SourceRecord)
            .where(SourceRecord.id == claimed.job.source_id)
            .options(
                raiseload('*'),
                selectinload(SourceRecord.candidates),
                selectinload(SourceRecord.candidate_runs).selectinload(ProviderCandidateRunRecord.candidates),
                selectinload(SourceRecord.tag_observations),
            )
        )
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def folder_members(self, folder: Path) -> tuple[SourceRecord, ...]:
        members = self.session.scalars(
            select(SourceRecord)
            .where(SourceRecord.source_path.startswith(f'{folder}/', autoescape=True))
            .where(SourceRecord.library_record_id.is_not(None))
            .where(SourceRecord.disappeared_at.is_(None))
            .where(SourceRecord.intake_state != 'replaced')
            .options(
                raiseload('*'),
                selectinload(SourceRecord.candidates),
                selectinload(SourceRecord.candidate_runs).selectinload(ProviderCandidateRunRecord.candidates),
                selectinload(SourceRecord.tag_observations),
            )
        ).all()
        return tuple(item for item in members if _folder_selection_root(item.source_path) == folder)

    def owned_source_path(self, source: SourceRecord) -> Path | QuarantineSource:
        try:
            return resolve_owned_source(source)
        except SourceBoundaryError as error:
            return QuarantineSource(source.id, f'root boundary: {error}')

    def changed(self, source: SourceRecord, path: Path) -> bool:
        if source.mtime_ns == 0:
            return False
        stat = path.stat()
        return (stat.st_dev, stat.st_ino, stat.st_size, stat.st_mtime_ns) != (
            source.device,
            source.inode,
            source.size_bytes,
            source.mtime_ns,
        )
