from __future__ import annotations

import logging
from collections.abc import Callable

import anyio
from anyio.to_thread import run_sync
from sqlalchemy.orm import Session

from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.settings import load_runtime_settings

LOGGER = logging.getLogger(__name__)
_MAX_WORKER_CONCURRENCY = 8


async def run_processing_worker(
    session_factory: Callable[[], Session], config: ProcessingConfig, poll_seconds: float = 1.0
) -> None:
    async with anyio.create_task_group() as task_group:
        for worker_slot in range(_MAX_WORKER_CONCURRENCY):
            _ = task_group.start_soon(_run_processing_worker_slot, session_factory, config, worker_slot, poll_seconds)


async def _run_processing_worker_slot(
    session_factory: Callable[[], Session], config: ProcessingConfig, worker_slot: int, poll_seconds: float
) -> None:
    while True:
        try:
            worker_concurrency = await run_sync(_worker_concurrency, session_factory)
            processed = (
                await run_sync(_run_processing_once, session_factory, config)
                if worker_slot < worker_concurrency
                else False
            )
        except Exception:  # noqa: BLE001
            LOGGER.exception('processing worker iteration failed; continuing poll loop')
            processed = False
        if not processed:
            await anyio.sleep(poll_seconds)


def _run_processing_once(session_factory: Callable[[], Session], config: ProcessingConfig) -> bool:
    with session_factory() as session:
        processed = ProcessingWorker(session, config).run_once()
        session.commit()
        return processed


def _worker_concurrency(session_factory: Callable[[], Session]) -> int:
    with session_factory() as session:
        return load_runtime_settings(session).worker_concurrency
