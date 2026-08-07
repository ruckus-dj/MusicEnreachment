from __future__ import annotations

import logging
from collections.abc import Callable

import anyio
from anyio.to_thread import run_sync
from sqlalchemy.orm import Session

from music_ingest.processing import ProcessingConfig, ProcessingWorker

LOGGER = logging.getLogger(__name__)


async def run_processing_worker(
    session_factory: Callable[[], Session], config: ProcessingConfig, poll_seconds: float = 1.0
) -> None:
    while True:
        try:
            processed = await run_sync(_run_processing_once, session_factory, config)
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
