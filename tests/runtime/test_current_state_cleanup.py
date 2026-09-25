from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime
from pathlib import Path
from threading import Event

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session, sessionmaker
from testcontainers.community.postgres import PostgresContainer

from music_ingest.api import create_app
from music_ingest.models import (
    Base,
    JobAttemptRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
    SourceLocationRecord,
    SourceRecord,
    SourceRootRecord,
    WebhookReceiptRecord,
)
from music_ingest.services.publication.locks import acquire_migration_lock
from music_ingest.services.reconciliation import current_state_cleanup


def _record(record_id: str, now: datetime) -> LibraryRecord:
    return LibraryRecord(id=record_id, created_at=now, updated_at=now)


def _source(source_id: str, record: LibraryRecord, root: SourceRootRecord) -> SourceRecord:
    return SourceRecord(
        id=source_id,
        identity_key=f'{source_id}:1:1',
        source_path=f'/incoming/{source_id}.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        mtime_ns=1,
        origin='manual',
        intake_state='present',
        source_root=root,
        library_record=record,
    )


def test_current_state_cleanup_is_dry_run_by_default_and_preserves_current_output(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "cleanup.db"}')
    _ = Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path='/incoming',
            enabled=True,
            scan_state='scanned',
            created_at=now,
            updated_at=now,
        )
        obsolete = _record('obsolete', now)
        obsolete_source = _source('obsolete-source', obsolete, root)
        published = _record('published', now)
        published_source = _source('published-source', published, root)
        canonical = _record('canonical', now)
        retired = _record('retired', now)
        live_source = _source('live-source', retired, root)
        live_source.locations.append(SourceLocationRecord(source_root=root, path='/incoming/live.flac'))
        session.add_all(
            (
                obsolete_source,
                published_source,
                live_source,
                canonical,
                LibraryPublicationRecord(
                    id='current',
                    library_record=published,
                    source=published_source,
                    path='/media/current.mka',
                    format_name='mka',
                    content_sha256='b' * 64,
                    state='current',
                    created_at=now,
                ),
                LibraryRecordConsolidationRecord(
                    retired_library_record_id=retired.id,
                    canonical_library_record_id=canonical.id,
                    sha256='c' * 64,
                    created_at=now,
                ),
            )
        )
        session.commit()

        preview = current_state_cleanup(session)
        assert preview.source_ids == ('obsolete-source', 'published-source')
        assert preview.library_record_ids == ('obsolete',)
        assert session.get(SourceRecord, 'obsolete-source') is not None

        applied = current_state_cleanup(session, apply=True)
        session.commit()

        assert applied.source_ids == preview.source_ids
        assert applied.library_record_ids == preview.library_record_ids
        assert session.get(SourceRecord, 'obsolete-source') is None
        assert session.get(LibraryRecord, 'obsolete') is None
        assert session.get(LibraryRecord, 'published') is not None
        assert session.get(LibraryRecord, 'canonical') is not None
        publication = session.scalars(select(LibraryPublicationRecord)).one()
        assert publication.source_id is None


def test_current_state_cleanup_api_requires_preview_then_applies_current_targets(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "cleanup-api.db"}')
    _ = Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path='/incoming',
            enabled=True,
            scan_state='scanned',
            created_at=now,
            updated_at=now,
        )
        session.add(_source('obsolete-source', _record('obsolete', now), root))
        session.commit()

    client = TestClient(create_app(sessionmaker(engine)))
    preview = client.post('/api/settings/maintenance/current-state/preview')

    assert preview.status_code == 200
    assert preview.json() == {'source_count': 1, 'library_record_count': 1, 'applied': False}
    with Session(engine) as session:
        assert session.get(SourceRecord, 'obsolete-source') is not None

    applied = client.post('/api/settings/maintenance/current-state/apply')

    assert applied.status_code == 200
    assert applied.json() == {'source_count': 1, 'library_record_count': 1, 'applied': True}
    assert client.post('/api/settings/maintenance/current-state/preview').json() == {
        'source_count': 0,
        'library_record_count': 0,
        'applied': False,
    }


def test_current_state_cleanup_removes_a_complete_consolidation_chain_in_one_apply(tmp_path: Path) -> None:
    # Given: an orphan retired record protects an otherwise orphan canonical record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "cleanup-chain.db"}')
    _ = Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        retired = _record('retired', now)
        canonical = _record('canonical', now)
        session.add_all(
            (
                retired,
                canonical,
                LibraryRecordConsolidationRecord(
                    retired_library_record_id=retired.id,
                    canonical_library_record_id=canonical.id,
                    sha256='c' * 64,
                    created_at=now,
                ),
            )
        )
        session.commit()

        # When: cleanup previews and applies the transitive orphan closure.
        preview = current_state_cleanup(session)
        applied = current_state_cleanup(session, apply=True)
        session.commit()

        # Then: both records are removed together and the next preview is empty.
        assert preview.library_record_ids == ('canonical', 'retired')
        assert applied.library_record_ids == preview.library_record_ids
        assert current_state_cleanup(session).library_record_ids == ()


@pytest.mark.postgres
def test_current_state_cleanup_deletes_orphan_history_with_postgres_constraints(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime.now(UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        _ = Base.metadata.create_all(engine)
        with Session(engine) as session:
            record = _record('orphan', now)
            revision = LibraryMetadataRevisionRecord(
                library_record=record,
                layer='final',
                revision=1,
                tags_json='{}',
                actor='manual',
                created_at=now,
            )
            session.add_all((record, revision))
            session.flush()
            publication = LibraryPublicationRecord(
                id='superseded',
                library_record=record,
                path='/media/old.mka',
                format_name='mka',
                content_sha256='d' * 64,
                metadata_revision=revision,
                state='superseded',
                created_at=now,
            )
            job = JobRecord(
                id='orphan-job',
                library_record_id=record.id,
                metadata_revision_id=revision.id,
                kind='selection_refresh',
                state='completed',
                created_at=now,
            )
            session.add_all(
                (
                    publication,
                    job,
                    JobAttemptRecord(
                        job=job,
                        attempt_number=1,
                        state='succeeded',
                        started_at=now,
                        finished_at=now,
                    ),
                    WebhookReceiptRecord(
                        event_fingerprint='orphan-receipt',
                        provider_name='test',
                        payload_json='{}',
                        received_at=now,
                        job=job,
                    ),
                    LibraryEventRecord(
                        library_record=record,
                        kind='obsolete',
                        state='complete',
                        details_json='{}',
                        created_at=now,
                    ),
                )
            )
            session.flush()
            record.lyrics_publication_id = publication.id
            session.commit()

            report = current_state_cleanup(session, apply=True)
            session.commit()

            assert report.library_record_ids == ('orphan',)
            assert session.get(LibraryRecord, 'orphan') is None
            assert session.get(JobRecord, 'orphan-job') is None
            assert session.scalars(select(LibraryPublicationRecord)).all() == []
            assert session.scalars(select(LibraryMetadataRevisionRecord)).all() == []
        engine.dispose()


@pytest.mark.postgres
def test_current_state_cleanup_waits_for_workers_and_clears_replacement_references(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: a worker transaction holds the shared gate while a live source points to a stale replacement.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime.now(UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        _ = Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path='/incoming',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            record = _record('record', now)
            live = _source('live', record, root)
            stale = _source('stale', record, root)
            live.replaced_by_source_id = stale.id
            live.locations.append(SourceLocationRecord(source_root=root, path='/incoming/live.flac'))
            session.add_all((live, stale))
            session.commit()

        holder_ready = Event()
        release_holder = Event()
        cleanup_finished = Event()

        def hold_worker_gate() -> None:
            with Session(engine) as session:
                acquire_migration_lock(session)
                holder_ready.set()
                assert release_holder.wait(timeout=5)
                session.commit()

        def apply_cleanup() -> None:
            assert holder_ready.wait(timeout=5)
            with Session(engine) as session:
                _ = current_state_cleanup(session, apply=True)
                session.commit()
                cleanup_finished.set()

        # When: cleanup starts before the worker transaction releases its shared advisory lock.
        with ThreadPoolExecutor(max_workers=2) as executor:
            holder = executor.submit(hold_worker_gate)
            cleanup = executor.submit(apply_cleanup)
            assert holder_ready.wait(timeout=5)
            assert not cleanup_finished.wait(timeout=0.1)
            release_holder.set()
            holder.result(timeout=5)
            cleanup.result(timeout=5)

        # Then: cleanup runs afterward, deletes the stale source, and clears the surviving self-reference.
        with Session(engine) as session:
            live = session.get_one(SourceRecord, 'live')
            assert session.get(SourceRecord, 'stale') is None
            assert live.replaced_by_source_id is None
        engine.dispose()
