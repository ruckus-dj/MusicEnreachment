from __future__ import annotations

import logging
from pathlib import Path

import anyio
import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord
from music_ingest.processing.scheduler import enqueue_reconciliation_scan, run_reconciliation_scheduler


def _session_factory(tmp_path: Path) -> object:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "scheduler.db"}')
    Base.metadata.create_all(engine)
    return lambda: Session(engine)


def test_enqueue_reconciliation_scan_when_called_persists_a_queued_job(tmp_path: Path) -> None:
    # Given: an empty job queue.
    session_factory = _session_factory(tmp_path)

    # When: a reconciliation scan is enqueued.
    enqueue_reconciliation_scan(session_factory)

    # Then: exactly one durable reconciliation_scan job is queued.
    with session_factory() as session:
        jobs = session.query(JobRecord).filter_by(kind='reconciliation_scan').all()
    assert len(jobs) == 1
    assert jobs[0].state == 'queued'


def test_run_reconciliation_scheduler_when_run_repeatedly_enqueues_one_scan_per_interval(
    tmp_path: Path,
) -> None:
    # Given: a scheduler configured to fire on every anyio checkpoint.
    session_factory = _session_factory(tmp_path)

    async def driver() -> None:
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(run_reconciliation_scheduler, session_factory, 0)
            await anyio.sleep(0.05)
            task_group.cancel_scope.cancel()

    anyio.run(driver)

    # Then: at least one reconciliation scan reached durable storage before cancellation.
    with session_factory() as session:
        jobs = session.query(JobRecord).filter_by(kind='reconciliation_scan').all()
    assert len(jobs) >= 1


def test_run_reconciliation_scheduler_when_enqueue_fails_logs_and_continues(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    # Given: a scheduler whose enqueue call always fails with a database error.
    from sqlalchemy.exc import SQLAlchemyError

    session_factory = _session_factory(tmp_path)
    calls = 0

    def failing_enqueue(_: object) -> None:
        nonlocal calls
        calls += 1
        raise SQLAlchemyError('boom')

    monkeypatch.setattr('music_ingest.processing.scheduler.enqueue_reconciliation_scan', failing_enqueue)

    async def driver() -> None:
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(run_reconciliation_scheduler, session_factory, 0)
            await anyio.sleep(0.05)
            task_group.cancel_scope.cancel()

    # When: the scheduler loop runs and the enqueue step raises repeatedly.
    with caplog.at_level(logging.ERROR):
        anyio.run(driver)

    # Then: the failure is logged instead of crashing the scheduler loop, and it keeps retrying.
    assert calls >= 1
    assert 'reconciliation scheduler enqueue failed' in caplog.text
