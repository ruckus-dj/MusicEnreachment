from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime
from typing import TYPE_CHECKING, Protocol, override

from sqlalchemy.orm import Session

from music_ingest.models.jobs import ClaimedJob

if TYPE_CHECKING:
    from music_ingest.processing.worker import ProcessingConfig


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


class JobHandler(Protocol):
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None: ...


@dataclass(frozen=True, slots=True)
class MethodJobHandler:
    """Compatibility adapter while legacy handlers are migrated out of the worker."""

    callback: Callable[[ClaimedJob, datetime], None]

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        self.callback(claimed, context.now)
