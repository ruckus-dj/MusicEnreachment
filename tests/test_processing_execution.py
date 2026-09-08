from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord, SourceRecord, SourceRootRecord, SourceTagRecord
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.processing.execution import (
    ExecutionContext,
    HandlerOutcome,
    ProcessingInfrastructureError,
    QuarantineSource,
)
from music_ingest.processing.handlers.initial import InitialHandler


@pytest.mark.parametrize(
    ('outcome', 'job_state', 'attempt_state', 'retains_evidence'),
    [
        ('success', 'completed', 'succeeded', True),
        ('quarantine', 'quarantined', 'quarantined', True),
        ('failure', 'queued', 'retry_wait', False),
    ],
)
def test_worker_finalizes_handler_result_without_losing_transaction_boundaries(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    outcome: str,
    job_state: str,
    attempt_state: str,
    retains_evidence: bool,
) -> None:
    config = ProcessingConfig(tmp_path / 'incoming', tmp_path / 'staging', tmp_path / 'media')
    config.incoming_root.mkdir()
    source_path = config.incoming_root / 'source.flac'
    source_path.write_bytes(b'immutable source')
    now = datetime.now(UTC)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "execution.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path=str(config.incoming_root),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        session.add(
            SourceRecord(
                id='source',
                source_root_id='root',
                source_path=str(source_path),
                device=1,
                inode=2,
                size_bytes=16,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
            )
        )
        session.add(JobRecord(id='job', source_id='source', kind='filesystem_scan', state='queued', created_at=now))
        session.commit()

    def handle(handler: InitialHandler, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome:
        staged = handler.staging.staging_directory(claimed.job.id) / 'partial.mka'
        staged.write_bytes(b'temporary output')
        context.session.add(
            SourceTagRecord(source_id='source', format_name='fixture', tag_name='TITLE', value='observed')
        )
        context.session.flush()
        assert claimed.attempt.state == 'running'
        if outcome == 'failure':
            raise ProcessingInfrastructureError('simulated failure after evidence flush')
        if outcome == 'quarantine':
            return QuarantineSource('source', 'simulated rejection after inspection')
        return None

    monkeypatch.setattr(InitialHandler, 'handle', handle)
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        job = session.get(JobRecord, 'job')
        assert job is not None and job.state == job_state
        assert job.attempts[0].state == attempt_state
        assert bool(session.scalars(select(SourceTagRecord)).all()) is retains_evidence
        # Only the caller commits the outer transaction, including the claim and finalization.
        session.rollback()

    with Session(engine) as session:
        job = session.get(JobRecord, 'job')
        assert job is not None and job.state == 'queued' and job.attempts == []
        assert session.scalars(select(SourceTagRecord)).all() == []
    assert not (config.staging_root / 'job').exists()
    assert source_path.read_bytes() == b'immutable source'
    engine.dispose()
