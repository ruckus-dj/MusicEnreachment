from __future__ import annotations

import shutil
from dataclasses import dataclass
from pathlib import Path

from sqlalchemy.orm import Session

from music_ingest.services.metadata import (
    allocate_unsorted_filename,
)
from music_ingest.workers.config import ProcessingConfig


@dataclass(frozen=True, slots=True)
class StagingWorkspace:
    session: Session
    config: ProcessingConfig

    def staging_directory(self, job_id: str) -> Path:
        directory = self.config.staging_root / job_id
        self.config.staging_root.mkdir(parents=True, exist_ok=True)
        if directory.exists():
            shutil.rmtree(directory)
        directory.mkdir()
        return directory

    def discard_staging(self, job_id: str) -> None:
        directory = self.config.staging_root / job_id
        if directory.is_dir():
            shutil.rmtree(directory)

    def allocate_unsorted_filename(self, suffix: str) -> str:
        allocator = self.config.unsorted_filename_allocator
        if allocator is not None:
            return allocator(suffix)
        return allocate_unsorted_filename(self.session, suffix)
