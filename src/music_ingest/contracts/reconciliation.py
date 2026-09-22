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
