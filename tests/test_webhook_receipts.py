from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.persistence.models import Base, JobRecord, WebhookReceiptRecord
from music_ingest.persistence.repository import (
    ReceiptReplayConflictError,
    WebhookReceiptInput,
    WebhookReceiptRepository,
)


def test_webhook_receipt_when_replayed_reuses_existing_receipt_and_job(tmp_path: Path) -> None:
    # Given: an existing receipt associated with the durable intake job.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "receipts.db"}')
    Base.metadata.create_all(engine)
    receipt = WebhookReceiptInput(
        event_fingerprint='a' * 64,
        provider_name='lidarr',
        payload_json='{"event":"Download"}',
        received_at=datetime(2026, 7, 29, tzinfo=UTC),
        job_id='job-1',
    )
    with Session(engine) as session:
        session.add(JobRecord(id='job-1', kind='intake', state='queued', created_at=receipt.received_at))
        repository = WebhookReceiptRepository(session)
        first = repository.record_or_reuse(receipt)
        session.commit()

        # When: the identical event is replayed.
        replayed = repository.record_or_reuse(receipt)

        # Then: the receipt and its original job are reused without an integrity error.
        assert replayed.id == first.id
        assert replayed.job_id == 'job-1'
        assert len(session.scalars(select(WebhookReceiptRecord)).all()) == 1


def test_webhook_receipt_when_same_fingerprint_has_different_payload_rejects_replay(tmp_path: Path) -> None:
    # Given: a persisted receipt.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "receipts.db"}')
    Base.metadata.create_all(engine)
    received_at = datetime(2026, 7, 29, tzinfo=UTC)
    with Session(engine) as session:
        repository = WebhookReceiptRepository(session)
        repository.record_or_reuse(WebhookReceiptInput('a' * 64, 'lidarr', '{"event":"Download"}', received_at))
        session.commit()

        # When: the fingerprint is replayed with changed durable content.
        with pytest.raises(ReceiptReplayConflictError):
            repository.record_or_reuse(WebhookReceiptInput('a' * 64, 'lidarr', '{"event":"Rename"}', received_at))

        # Then: the original receipt remains the only durable record.
        assert len(session.scalars(select(WebhookReceiptRecord)).all()) == 1
