from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    JobRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceRootRecord,
    SourceTagRecord,
    StorageConfigRecord,
)
from music_ingest.repositories.jobs import ClaimedJob
from music_ingest.services.settings import SettingKey
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.execution import (
    ExecutionContext,
    HandlerOutcome,
    ProcessingInfrastructureError,
    QuarantineSource,
)
from music_ingest.workers.handlers.initial import InitialHandler
from music_ingest.workers.handlers.reconciliation import ReconciliationHandler
from music_ingest.workers.worker import ProcessingWorker


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
        # Worker checkpoints survive a later caller rollback.
        session.rollback()

    with Session(engine) as session:
        job = session.get(JobRecord, 'job')
        assert job is not None and job.state == job_state
        assert job.attempts[0].state == attempt_state
        assert bool(session.scalars(select(SourceTagRecord)).all()) is retains_evidence
    assert not (config.staging_root / 'job').exists()
    assert source_path.read_bytes() == b'immutable source'
    engine.dispose()


def test_worker_reloads_settings_and_output_root_between_attempts_only(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    config = ProcessingConfig(tmp_path / 'incoming', tmp_path / 'staging', tmp_path / 'media')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "settings.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    observed: list[tuple[float, Path]] = []

    def handle(handler: ReconciliationHandler, claimed: ClaimedJob, context: ExecutionContext) -> None:
        _ = handler, claimed
        observed.append((context.settings.confidence_threshold(), context.config.media_root))
        threshold = context.session.get(RuntimeSettingRecord, SettingKey.CONFIDENCE_THRESHOLD.value)
        storage = context.session.get(StorageConfigRecord, 1)
        assert threshold is not None and storage is not None
        threshold.value = '0.95'
        storage.output_root = str(tmp_path / 'moved-media')
        context.session.flush()
        assert (context.settings.confidence_threshold(), context.config.media_root) == observed[-1]

    monkeypatch.setattr(ReconciliationHandler, 'handle', handle)
    with Session(engine) as session:
        session.add(RuntimeSettingRecord(key=SettingKey.CONFIDENCE_THRESHOLD.value, value='0.75', updated_at=now))
        session.add(StorageConfigRecord(id=1, output_root=str(config.media_root), updated_at=now))
        session.commit()
        worker = ProcessingWorker(session, config)
        for number in range(2):
            session.add(JobRecord(id=f'job-{number}', kind='reconciliation_scan', state='queued', created_at=now))
            session.commit()
            assert worker.run_once()
            session.commit()

    assert observed == [(0.75, config.media_root), (0.95, tmp_path / 'moved-media')]
    engine.dispose()
