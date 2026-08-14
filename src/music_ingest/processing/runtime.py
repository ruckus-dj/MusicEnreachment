from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from threading import Lock

import anyio
from anyio.to_thread import run_sync
from sqlalchemy.orm import Session

from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.settings import load_runtime_settings

LOGGER = logging.getLogger(__name__)
_MAX_WORKER_CONCURRENCY = 8


@dataclass(frozen=True, slots=True)
class WorkerSlotSnapshot:
    slot: int
    state: str
    observed_at: datetime
    error: str | None
    job_id: str | None
    job_kind: str | None


class ProcessingRuntimeMonitor:
    def __init__(self) -> None:
        self._lock: Lock = Lock()
        self._slots: dict[int, WorkerSlotSnapshot] = {}

    def observe(
        self,
        slot: int,
        state: str,
        error: str | None = None,
        job_id: str | None = None,
        job_kind: str | None = None,
    ) -> None:
        with self._lock:
            self._slots[slot] = WorkerSlotSnapshot(slot, state, datetime.now(UTC), error, job_id, job_kind)

    def snapshots(self) -> tuple[WorkerSlotSnapshot, ...]:
        with self._lock:
            return tuple(self._slots[index] for index in sorted(self._slots))


async def run_processing_worker(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    poll_seconds: float = 1.0,
    monitor: ProcessingRuntimeMonitor | None = None,
) -> None:
    async with anyio.create_task_group() as task_group:
        for worker_slot in range(_MAX_WORKER_CONCURRENCY):
            if monitor is None:
                _ = task_group.start_soon(
                    _run_processing_worker_slot, session_factory, config, worker_slot, poll_seconds
                )
            else:
                _ = task_group.start_soon(
                    _run_processing_worker_slot, session_factory, config, worker_slot, poll_seconds, monitor
                )


async def _run_processing_worker_slot(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    worker_slot: int,
    poll_seconds: float,
    monitor: ProcessingRuntimeMonitor | None = None,
) -> None:
    while True:
        try:
            worker_concurrency = await run_sync(_worker_concurrency, session_factory)
            if worker_slot >= worker_concurrency:
                if monitor is not None:
                    monitor.observe(worker_slot, 'disabled')
                processed = False
            else:
                if monitor is not None:
                    monitor.observe(worker_slot, 'processing')
                processed = await run_sync(_run_processing_once, session_factory, config, worker_slot, monitor)
                if monitor is not None:
                    monitor.observe(worker_slot, 'idle')
        except Exception as error:  # noqa: BLE001
            LOGGER.exception('processing worker iteration failed; continuing poll loop')
            if monitor is not None:
                monitor.observe(worker_slot, 'error', str(error))
            processed = False
        if not processed:
            await anyio.sleep(poll_seconds)


def _run_processing_once(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    worker_slot: int,
    monitor: ProcessingRuntimeMonitor | None,
) -> bool:
    with session_factory() as session:

        def observe_claim(job_id: str, job_kind: str) -> None:
            if monitor is not None:
                monitor.observe(worker_slot, 'processing', job_id=job_id, job_kind=job_kind)

        processed = ProcessingWorker(session, config).run_once(on_claimed=observe_claim)
        session.commit()
        return processed


def _worker_concurrency(session_factory: Callable[[], Session]) -> int:
    with session_factory() as session:
        return load_runtime_settings(session).worker_concurrency
