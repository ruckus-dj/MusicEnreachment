from __future__ import annotations

import logging
from collections.abc import Callable
from datetime import UTC, datetime

import anyio
from anyio.to_thread import run_sync
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session

from music_ingest.models.jobs import JobRepository

LOGGER = logging.getLogger(__name__)


def enqueue_reconciliation_scan(session_factory: Callable[[], Session]) -> None:
    with session_factory() as session:
        _ = JobRepository(session).enqueue_reconciliation_scan(datetime.now(UTC))
        session.commit()


async def run_reconciliation_scheduler(session_factory: Callable[[], Session], interval_seconds: int) -> None:
    while True:
        await anyio.sleep(interval_seconds)
        try:
            await run_sync(enqueue_reconciliation_scan, session_factory)
        except SQLAlchemyError:
            LOGGER.exception('reconciliation scheduler enqueue failed; continuing next interval')
