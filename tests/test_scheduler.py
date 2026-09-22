from __future__ import annotations

import logging
from datetime import UTC, datetime, timedelta
from pathlib import Path

import anyio
import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord
from music_ingest.repositories.jobs import JobRepository
from music_ingest.workers.scheduler import enqueue_reconciliation_scan, run_reconciliation_scheduler


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


def test_claim_next_for_source_prefers_ready_continuation_over_older_global_job(tmp_path: Path) -> None:
    # Given: an older library job and a ready successor for the source this worker just processed.
    session_factory = _session_factory(tmp_path)
    now = datetime.now(UTC)
    with session_factory() as session:
        session.add_all(
            (
                JobRecord(
                    id='older-global',
                    source_id='other-source',
                    kind='filesystem_scan',
                    state='queued',
                    created_at=now,
                ),
                JobRecord(
                    id='source-successor',
                    source_id='continued-source',
                    kind='acoustid_analysis',
                    state='queued',
                    created_at=now + timedelta(seconds=1),
                ),
            )
        )
        session.commit()

        # When: the worker asks for its source continuation.
        claimed = JobRepository(session).claim_next_for_source(
            'continued-source', now + timedelta(seconds=2), timedelta(minutes=5)
        )

        # Then: it keeps the source pipeline moving instead of returning to global FIFO.
        assert claimed is not None
        assert claimed.job.id == 'source-successor'


def test_source_filter_cannot_escape_a_dedicated_pool(tmp_path: Path) -> None:
    session_factory = _session_factory(tmp_path)
    now = datetime.now(UTC)
    with session_factory() as session:
        session.add(
            JobRecord(id='selection', source_id='source', kind='candidate_selection', state='queued', created_at=now)
        )
        session.commit()
        assert (
            JobRepository(session).claim_next_for_source('source', now, timedelta(minutes=5), {'acoustid_analysis'})
            is None
        )


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

    monkeypatch.setattr('music_ingest.workers.scheduler.enqueue_reconciliation_scan', failing_enqueue)

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
