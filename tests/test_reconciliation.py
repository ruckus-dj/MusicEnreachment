from __future__ import annotations

from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord, SourceRecord
from music_ingest.reconciliation import reconcile_incoming


def test_reconcile_incoming_detects_added_changed_and_removed_files(tmp_path: Path) -> None:
    # Given: an incoming folder with two files and an empty durable catalog.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    first = incoming / 'first.flac'
    second = incoming / 'second.flac'
    _ = first.write_bytes(b'first-v1')
    _ = second.write_bytes(b'second-v1')

    with Session(engine) as session:
        initial = reconcile_incoming(session, incoming)
        session.commit()

        # When: one file is changed, one is removed, and one is added.
        _ = first.write_bytes(b'first-v2')
        second.unlink()
        third = incoming / 'third.flac'
        _ = third.write_bytes(b'third-v1')
        current = reconcile_incoming(session, incoming)
        session.commit()

        _ = first.unlink()
        after_change_removed = reconcile_incoming(session, incoming)
        session.commit()

        # Then: the filesystem delta is durable and each new content version is queued once.
        assert initial.added == 2
        assert current.added == 1
        assert current.changed == 1
        assert current.removed == 1
        assert after_change_removed.removed == 1
        assert len(session.scalars(select(SourceRecord)).all()) == 4
        assert len(session.scalars(select(JobRecord)).all()) == 4


def test_reconcile_incoming_requeues_present_quarantined_jobs(tmp_path: Path) -> None:
    # Given: a present source whose previous processing attempt was quarantined.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation-retry.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    source_path = incoming / 'retry.flac'
    _ = source_path.write_bytes(b'retry-source')

    with Session(engine) as session:
        _ = reconcile_incoming(session, incoming)
        session.commit()
        source = session.scalars(select(SourceRecord)).one()
        job = session.scalars(select(JobRecord)).one()
        source.intake_state = 'quarantined'
        job.state = 'quarantined'
        session.commit()

        # When: the operator scans the unchanged incoming tree after correcting the cause.
        result = reconcile_incoming(session, incoming)
        session.commit()

        # Then: the existing job is reactivated without creating a duplicate.
        assert result.queued_jobs == 1
        assert job.state == 'queued'
        assert len(session.scalars(select(JobRecord)).all()) == 1
