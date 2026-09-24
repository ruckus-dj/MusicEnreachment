from typing import ClassVar

from pydantic import BaseModel, ConfigDict


class ScanResult(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    added: int
    changed: int
    removed: int
    moved: int
    unchanged: int
    queued_jobs: int


class ScanJobResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    job_id: str
    state: str
    result: ScanResult | None = None


class PublicationReconciliationResult(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    removed_files: int
    removed_directories: int
    preserved_nfo: int
    missing_publications: int
    queued_jobs: int
    already_queued: int
    deferred_publications: int
    unsafe_entries: int


class PublicationReconciliationJobResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    job_id: str
    state: str
    result: PublicationReconciliationResult | None = None
