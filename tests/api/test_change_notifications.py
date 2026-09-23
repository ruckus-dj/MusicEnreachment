from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base, JobRecord, SourceRecord, SourceRootRecord
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker


def test_change_notification_queues_standard_reconciliation_without_provider_provenance(tmp_path: Path) -> None:
    # Given: a generic external notification after a file arrives below an enabled source root.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "notification.db"}')
    Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    source_path = incoming / 'Artist' / 'Release' / '01.flac'
    source_path.parent.mkdir(parents=True)
    source_path.write_bytes(b'new source')
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='incoming',
                display_name='Incoming',
                canonical_path=str(incoming),
                enabled=True,
                scan_state='never_scanned',
                created_at=now,
                updated_at=now,
            )
        )
        session.commit()
    client = TestClient(create_app(lambda: Session(engine)))

    # When: any downloader posts its completion message, including an optional path hint.
    response = client.post('/api/intake/notification', json={'paths': [str(source_path)]})

    # Then: the service schedules its source-agnostic reconciliation flow, with no webhook receipt or provider origin.
    assert response.status_code == 202
    job_id = response.json()['job_id']
    with Session(engine) as session:
        job = session.get(JobRecord, job_id)
        assert job is not None
        assert job.kind == 'reconciliation_scan'
        worker = ProcessingWorker(
            session,
            ProcessingConfig(incoming_root=incoming, staging_root=tmp_path / 'staging', media_root=tmp_path / 'media'),
        )
        assert worker.run_once()
        session.commit()
    with Session(engine) as session:
        source = session.scalars(select(SourceRecord)).one()
        assert source.origin == 'manual'
        assert source.source_path == str(source_path)
