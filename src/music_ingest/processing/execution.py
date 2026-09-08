from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import TYPE_CHECKING, Protocol, override

from sqlalchemy.orm import Session

from music_ingest.models.jobs import ClaimedJob

if TYPE_CHECKING:
    from music_ingest.processing.config import ProcessingConfig
    from music_ingest.processing.support.settings import RuntimeProcessingSettings


@dataclass(frozen=True, slots=True)
class ProcessingInfrastructureError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


@dataclass(frozen=True, slots=True)
class ExecutionContext:
    """Stable infrastructure dependencies for one claimed job execution."""

    session: Session
    config: ProcessingConfig
    now: datetime
    settings: RuntimeProcessingSettings


@dataclass(frozen=True, slots=True)
class QuarantineSource:
    source_id: str
    reason: str


@dataclass(frozen=True, slots=True)
class ChangedSource:
    source_id: str
    path: Path


type HandlerOutcome = QuarantineSource | ChangedSource | None


class JobHandler(Protocol):
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome: ...
