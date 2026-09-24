from __future__ import annotations

import json
from argparse import Namespace
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from pathlib import Path
from threading import Barrier, Event

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, event, inspect, select, text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from alembic import command
from music_ingest.models import (
    Base,
    CandidateRecord,
    JobAttemptRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.repositories.jobs import ClaimedJob, JobRepository
from music_ingest.services.association import (
    AutomaticAssociationRequest,
    ManualAssociationRequest,
    RecordingAssociationService,
)
from music_ingest.services.publication import (
    acquire_publication_destination_lock,
    try_acquire_publication_destination_lock,
)
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.support.sources import SourceAccess
from music_ingest.workers.worker import ProcessingWorker
from tests.support.paths import ALEMBIC_DIRECTORY

_MIGRATION_DIRECTORY = ALEMBIC_DIRECTORY
_BASE_REVISION = '20260810_0002'
_HEAD_REVISION = '20260924_0032'


@pytest.mark.postgres
def test_dedicated_pools_enter_handlers_concurrently_without_global_storage_lock(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime.now(UTC)
    with PostgresContainer('postgres:17') as postgres:
        engine = create_engine(postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg'))
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            for kind in ('selection_refresh', 'lrclib_fetch'):
                session.add(LibraryRecord(id=kind, created_at=now, updated_at=now))
                session.flush()
                session.add(JobRecord(id=kind, kind=kind, library_record_id=kind, state='queued', created_at=now))
            session.commit()
        entered = Barrier(2)

        def run(kind: str) -> bool:
            with Session(engine) as session:

                def claimed(job_id: str, job_kind: str) -> None:
                    assert job_kind == kind
                    entered.wait(timeout=5)
                    # Recovery/publication's exclusive lock must not span unrelated handlers.
                    if kind == 'lrclib_fetch':
                        return
                    with Session(engine) as probe:
                        assert probe.scalar(text('SELECT pg_try_advisory_xact_lock(732014901)'))
                        # But migration must still exclude all active handlers.
                        assert not probe.scalar(text('SELECT pg_try_advisory_xact_lock(732014902)'))

                worker = ProcessingWorker(
                    session,
                    ProcessingConfig(
                        incoming_root=tmp_path / 'incoming',
                        staging_root=tmp_path / 'staging',
                        media_root=tmp_path / 'media',
                    ),
                )
                result = worker.run_once(allowed_kinds={kind}, on_claimed=claimed)
                session.commit()
                return result

        with ThreadPoolExecutor(max_workers=2) as executor:
            assert all(executor.map(run, ('selection_refresh', 'lrclib_fetch')))
        engine.dispose()


@pytest.mark.postgres
def test_publication_destination_lock_when_two_transactions_target_one_release_blocks_second_worker(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: two independent PostgreSQL transactions publishing to one release directory.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        destination = Path('/managed/Artist/Album')
        holder_acquired = Event()
        release_holder = Event()
        contender_acquired = Event()

        def hold_destination_lock() -> None:
            with Session(engine) as session:
                acquire_publication_destination_lock(session, destination)
                holder_acquired.set()
                assert release_holder.wait(timeout=5)
                session.commit()

        def acquire_contended_lock() -> None:
            assert holder_acquired.wait(timeout=5)
            with Session(engine) as session:
                acquire_publication_destination_lock(session, destination)
                contender_acquired.set()
                session.rollback()

        with ThreadPoolExecutor(max_workers=2) as executor:
            holder = executor.submit(hold_destination_lock)
            assert holder_acquired.wait(timeout=5)
            contender = executor.submit(acquire_contended_lock)

            # When: the second transaction requests the same destination before the first commits.
            assert not contender_acquired.wait(timeout=0.1)
            release_holder.set()
            holder.result(timeout=5)

            # Then: it acquires the lock only after the first transaction releases it.
            contender.result(timeout=5)
            assert contender_acquired.is_set()
        engine.dispose()


@pytest.mark.postgres
def test_publication_destination_try_lock_when_contended_returns_without_blocking_worker(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: one PostgreSQL transaction already owns a managed release destination lock.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        destination = Path('/managed/Artist/Album')
        with Session(engine) as holder:
            acquire_publication_destination_lock(holder, destination)
            with Session(engine) as contender:
                # When: a second worker attempts the non-blocking lock.
                acquired = try_acquire_publication_destination_lock(contender, destination)

                # Then: the worker can retry another job instead of waiting on the destination.
                assert not acquired
        engine.dispose()


@pytest.mark.postgres
def test_selection_refresh_when_concurrent_calls_coalesces_at_postgresql_index(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: two independent transactions racing to queue one record refresh.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            session.add(LibraryRecord(id='record-race', created_at=now, updated_at=now))
            session.commit()
        barrier = Barrier(2)

        def enqueue() -> bool:
            with Session(engine) as session:
                barrier.wait()
                created = JobRepository(session).enqueue_selection_refresh('record-race', now)
                session.commit()
                return created is not None

        # When: both transactions contend for the partial unique active-job index.
        with ThreadPoolExecutor(max_workers=2) as executor:
            created = tuple(executor.map(lambda _: enqueue(), range(2)))

        # Then: the durable state contains one active refresh and no caller leaked IntegrityError.
        with Session(engine) as session:
            active = session.query(JobRecord).filter_by(library_record_id='record-race', kind='selection_refresh').all()
            assert sum(created) == 1
            assert len(active) == 1
        engine.dispose()


@pytest.mark.postgres
def test_lrclib_fetch_when_concurrent_calls_coalesces_at_postgresql_index(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: two independent transactions racing to queue one record's lyric fetch.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 9, 11, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            session.add(LibraryRecord(id='record-lrclib-race', created_at=now, updated_at=now))
            session.commit()
        barrier = Barrier(2)

        def enqueue() -> bool:
            with Session(engine) as session:
                barrier.wait()
                created = JobRepository(session).enqueue_lrclib_fetch('record-lrclib-race', now)
                session.commit()
                return created is not None

        # When: both transactions contend for the partial unique active-job index.
        with ThreadPoolExecutor(max_workers=2) as executor:
            created = tuple(executor.map(lambda _: enqueue(), range(2)))

        # Then: one active lyric fetch persists and the duplicate caller does not leak IntegrityError.
        with Session(engine) as session:
            active = (
                session.query(JobRecord).filter_by(library_record_id='record-lrclib-race', kind='lrclib_fetch').all()
            )
            assert sum(created) == 1
            assert len(active) == 1
        engine.dispose()


@pytest.mark.postgres
def test_selection_refresh_when_two_workers_race_preserves_lock_and_event_order(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            session.add(LibraryRecord(id='record-refresh-race', created_at=now, updated_at=now))
            assert JobRepository(session).enqueue_selection_refresh('record-refresh-race', now) is not None
            session.commit()
        barrier = Barrier(2)

        def run_refresh() -> bool:
            with Session(engine) as session:
                barrier.wait()
                completed = ProcessingWorker(
                    session,
                    ProcessingConfig(
                        incoming_root=Path.cwd() / 'incoming',
                        staging_root=Path.cwd() / 'staging',
                        media_root=Path.cwd() / 'media',
                    ),
                ).run_once()
                session.commit()
                return completed

        with ThreadPoolExecutor(max_workers=2) as executor:
            completed = tuple(executor.map(lambda _: run_refresh(), range(2)))

        with Session(engine) as session:
            assert sum(completed) == 1
            events = (
                session.query(LibraryEventRecord)
                .filter_by(library_record_id='record-refresh-race')
                .order_by(LibraryEventRecord.id)
                .all()
            )
            assert [(event.kind, event.state) for event in events] == [
                ('selection_refresh_no_eligible_source', 'complete')
            ]
        engine.dispose()


@pytest.mark.postgres
def test_process_analysis_source_lock_when_two_transactions_target_one_source_blocks_second_worker(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: analysis has cached a source while another transaction holds its row lock.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            session.add(
                SourceRootRecord(
                    id='root-race',
                    display_name='Incoming',
                    canonical_path='/incoming',
                    enabled=True,
                    scan_state='scanned',
                    created_at=now,
                    updated_at=now,
                )
            )
            session.add(
                SourceRecord(
                    id='source-race',
                    source_path='/incoming/song.flac',
                    device=1,
                    inode=2,
                    size_bytes=3,
                    sha256='a' * 64,
                    origin='manual',
                    intake_state='present',
                    source_root_id='root-race',
                )
            )
            session.commit()
        holder_acquired = Event()
        release_holder = Event()
        contender_acquired = Event()
        contender_started = Event()

        def hold_source_lock() -> None:
            with Session(engine) as session:
                source = session.scalar(select(SourceRecord).where(SourceRecord.id == 'source-race').with_for_update())
                assert source is not None
                source.origin = 'updated'
                holder_acquired.set()
                assert release_holder.wait(timeout=5)
                session.commit()

        def acquire_contended_lock() -> None:
            assert holder_acquired.wait(timeout=5)
            with Session(engine) as session:
                cached = session.get(SourceRecord, 'source-race')
                assert cached is not None and cached.origin == 'manual'
                claimed = ClaimedJob(JobRecord(source_id='source-race'), JobAttemptRecord())
                contender_started.set()
                source = SourceAccess(session).locked_source(claimed)
                assert source is cached and source.origin == 'updated'
                contender_acquired.set()
                session.rollback()

        with ThreadPoolExecutor(max_workers=2) as executor:
            holder = executor.submit(hold_source_lock)
            assert holder_acquired.wait(timeout=5)
            contender = executor.submit(acquire_contended_lock)

            # When: the second transaction requests the same source record before the first commits.
            assert contender_started.wait(timeout=5)
            assert not contender_acquired.wait(timeout=0.1)
            release_holder.set()
            holder.result(timeout=5)

            # Then: it acquires the lock and refreshes the cached source after the holder commits.
            contender.result(timeout=5)
            assert contender_acquired.is_set()
        engine.dispose()


@pytest.mark.postgres
def test_selection_refresh_when_retried_claim_loads_one_record_target(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: one queued record-target refresh with a failed first attempt ready for retry.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            session.add(LibraryRecord(id='record-retry', created_at=now, updated_at=now))
            job = JobRepository(session).enqueue_selection_refresh('record-retry', now)
            assert job is not None
            session.commit()
            claimed = JobRepository(session).claim_next(now, timedelta(minutes=5))
            assert claimed is not None
            JobRepository(session).retry(claimed, now, timedelta(0), 3, 'transient')
            session.commit()

        with Session(engine) as session:
            # When: the retry is claimed from PostgreSQL.
            claimed = JobRepository(session).claim_next(now, timedelta(minutes=5))

            # Then: exactly one active job and one record-target claim are loaded.
            assert claimed is not None
            assert claimed.job.library_record_id == 'record-retry'
            assert claimed.job.source_id is None
            assert claimed.attempt.attempt_number == 2
            assert (
                session.query(JobRecord)
                .filter_by(library_record_id='record-retry', kind='selection_refresh')
                .filter(JobRecord.state.in_(['queued', 'running']))
                .count()
                == 1
            )
            session.rollback()
        engine.dispose()


@pytest.mark.postgres
def test_recording_association_when_two_sources_race_converges_on_one_record(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: two separately attached sources with durable confirmation for one recording MBID.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    evidence = (
        '{"provider":"musicbrainz","score":0.98,"tags":{"MUSICBRAINZ_TRACKID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
    )
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='legacy',
                display_name='legacy',
                canonical_path='/sources/legacy',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            records = tuple(LibraryRecord(id=f'record-{index}', created_at=now, updated_at=now) for index in (1, 2))
            sources = tuple(
                SourceRecord(
                    id=f'source-{index}',
                    source_path=f'/sources/legacy/{index}.flac',
                    device=index,
                    inode=index,
                    size_bytes=1,
                    sha256=str(index) * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    source_root=root,
                    library_record=records[index - 1],
                    provider_attempts=[
                        ProviderAttemptRecord(
                            provider_name='musicbrainz',
                            outcome='musicbrainzmatch',
                            snapshot_sha256='a' * 64,
                            snapshot='{}',
                        )
                    ],
                    candidates=[CandidateRecord(candidate_key=f'release-{index}', evidence=evidence)],
                )
                for index in (1, 2)
            )
            session.add_all((root, *records, *sources))
            session.commit()
        barrier = Barrier(2)

        def associate(source_id: str) -> str:
            with Session(engine) as session:
                barrier.wait()
                result = RecordingAssociationService(session).associate_verified_manual(
                    ManualAssociationRequest(source_id, recording_mbid, now, release_mbid='release-id')
                )
                session.commit()
                assert result is not None
                return result.library_record_id

        # When: both transactions create or locate the same recording aggregate simultaneously.
        with ThreadPoolExecutor(max_workers=2) as executor:
            resolved = tuple(executor.map(associate, ('source-1', 'source-2')))

        # Then: the unique MBID record is shared and neither transaction leaks IntegrityError.
        with Session(engine) as session:
            records = (
                session.query(LibraryRecord)
                .filter_by(musicbrainz_recording_id=recording_mbid, musicbrainz_release_id='release-id')
                .all()
            )
            sources = session.query(SourceRecord).filter(SourceRecord.id.in_(('source-1', 'source-2'))).all()
            assert len(records) == 1
            assert set(resolved) == {records[0].id}
            assert {source.library_record_id for source in sources} == {records[0].id}
        engine.dispose()


@pytest.mark.postgres
def test_automatic_association_batch_when_eight_transactions_race_converges_without_deadlock(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 9, 24, tzinfo=UTC)
    member_count = 8
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='batch-race-root',
                display_name='batch-race-root',
                canonical_path='/sources/batch-race',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            sources = tuple(
                SourceRecord(
                    id=f'batch-race-source-{index}',
                    source_path=f'/sources/batch-race/{index}.flac',
                    device=1,
                    inode=index,
                    size_bytes=1,
                    sha256=f'{index:064x}',
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    media_codec='FLAC',
                    media_bit_depth=16,
                    media_sample_rate=44_100,
                    media_channels=2,
                    source_root=root,
                    library_record=LibraryRecord(id=f'batch-race-record-{index}', created_at=now, updated_at=now),
                )
                for index in range(member_count)
            )
            session.add_all((root, *sources))
            session.commit()

        requests = tuple(
            AutomaticAssociationRequest(
                source_id=f'batch-race-source-{index}',
                recording_mbid=f'batch-race-recording-{index}',
                score=1.0,
                confidence_threshold=0.9,
                evidence_json='{}',
                now=now,
                release_mbid='batch-race-release',
            )
            for index in range(member_count)
        )
        barrier = Barrier(member_count)

        def associate(worker_index: int) -> dict[str, str]:
            worker_requests = requests if worker_index % 2 == 0 else tuple(reversed(requests))
            with Session(engine) as session:
                barrier.wait(timeout=10)
                results = RecordingAssociationService(session).associate_automatic_batch(worker_requests)
                session.commit()
                return {
                    request.source_id: result.library_record_id
                    for request, result in zip(worker_requests, results, strict=True)
                    if result is not None
                }

        with ThreadPoolExecutor(max_workers=member_count) as executor:
            resolved = tuple(executor.map(associate, range(member_count)))

        assert all(mapping == resolved[0] for mapping in resolved)
        with Session(engine) as session:
            stored_sources = tuple(
                session.scalars(
                    select(SourceRecord)
                    .where(SourceRecord.id.in_(tuple(sorted(resolved[0]))))
                    .order_by(SourceRecord.id)
                ).all()
            )
            assert {source.id: source.library_record_id for source in stored_sources} == resolved[0]
            assert (
                session.query(LibraryRecord).filter_by(musicbrainz_release_id='batch-race-release').count()
                == member_count
            )
        engine.dispose()


@pytest.mark.postgres
def test_single_association_refreshes_preloaded_source_after_lock_is_acquired(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 9, 24, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='source-race-root',
                display_name='source-race-root',
                canonical_path='/sources/source-race',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            original = LibraryRecord(id='source-race-original', created_at=now, updated_at=now)
            current = LibraryRecord(id='source-race-current', created_at=now, updated_at=now)
            target = LibraryRecord(
                id='source-race-target',
                musicbrainz_recording_id='source-race-recording',
                musicbrainz_release_id='source-race-release',
                match_state='matched',
                created_at=now,
                updated_at=now,
            )
            session.add_all(
                (
                    root,
                    original,
                    current,
                    target,
                    SourceRecord(
                        id='source-race-source',
                        source_path='/sources/source-race/source.flac',
                        device=1,
                        inode=1,
                        size_bytes=1,
                        sha256='d' * 64,
                        duration_seconds=180,
                        origin='manual',
                        intake_state='present',
                        source_root=root,
                        library_record=original,
                    ),
                )
            )
            session.commit()

        reassignment_committed = False

        def reassign_source_before_lock(
            _connection: object,
            _cursor: object,
            statement: str,
            _parameters: object,
            _context: object,
            _executemany: bool,
        ) -> None:
            nonlocal reassignment_committed
            if reassignment_committed or 'FROM source_records' not in statement or 'FOR UPDATE' not in statement:
                return
            reassignment_committed = True
            with engine.begin() as writer:
                writer.execute(
                    text(
                        "UPDATE source_records SET library_record_id = 'source-race-current' "
                        "WHERE id = 'source-race-source'"
                    )
                )

        with Session(engine) as session:
            preloaded = session.get(SourceRecord, 'source-race-source')
            assert preloaded is not None
            assert preloaded.library_record_id == 'source-race-original'
            event.listen(engine, 'before_cursor_execute', reassign_source_before_lock)
            try:
                result = RecordingAssociationService(session).associate_verified_manual(
                    ManualAssociationRequest(
                        preloaded.id,
                        'source-race-recording',
                        now + timedelta(seconds=1),
                        release_mbid='source-race-release',
                    )
                )
                session.commit()
            finally:
                event.remove(engine, 'before_cursor_execute', reassign_source_before_lock)

        assert reassignment_committed
        assert result.library_record_id == 'source-race-target'
        assert result.moved_from_record_id == 'source-race-current'
        with Session(engine) as session:
            event_record_ids = set(
                session.scalars(
                    select(LibraryEventRecord.library_record_id).where(
                        LibraryEventRecord.source_id == 'source-race-source'
                    )
                ).all()
            )
            assert event_record_ids == {'source-race-current', 'source-race-target'}
        engine.dispose()


@pytest.mark.postgres
def test_single_association_refreshes_target_metadata_after_lock_is_acquired(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 9, 24, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='metadata-race-root',
                display_name='metadata-race-root',
                canonical_path='/sources/metadata-race',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            previous = LibraryRecord(id='metadata-race-previous', created_at=now, updated_at=now)
            target = LibraryRecord(
                id='metadata-race-target',
                musicbrainz_recording_id='metadata-race-recording',
                musicbrainz_release_id='metadata-race-release',
                match_state='matched',
                created_at=now,
                updated_at=now,
            )
            source = SourceRecord(
                id='metadata-race-source',
                source_path='/sources/metadata-race/source.flac',
                device=1,
                inode=1,
                size_bytes=1,
                sha256='c' * 64,
                duration_seconds=180,
                origin='manual',
                intake_state='present',
                source_root=root,
                library_record=previous,
            )
            session.add_all(
                (
                    root,
                    previous,
                    target,
                    source,
                    LibraryMetadataRevisionRecord(
                        library_record=target,
                        layer='final',
                        revision=1,
                        tags_json=json.dumps({'TITLE': 'Stale provider title'}),
                        actor='provider',
                        created_at=now,
                    ),
                )
            )
            session.commit()

        revision_committed = False

        def commit_provider_revision_before_record_lock(
            _connection: object,
            _cursor: object,
            statement: str,
            _parameters: object,
            _context: object,
            _executemany: bool,
        ) -> None:
            nonlocal revision_committed
            if revision_committed or 'FROM library_records' not in statement or 'FOR UPDATE' not in statement:
                return
            revision_committed = True
            with Session(engine) as writer:
                writer.add(
                    LibraryMetadataRevisionRecord(
                        library_record_id='metadata-race-target',
                        layer='final',
                        revision=2,
                        tags_json=json.dumps({'TITLE': 'Current provider title'}),
                        actor='provider',
                        created_at=now + timedelta(seconds=1),
                    )
                )
                writer.commit()

        event.listen(engine, 'before_cursor_execute', commit_provider_revision_before_record_lock)
        try:
            with Session(engine) as session:
                result = RecordingAssociationService(session).associate_verified_manual(
                    ManualAssociationRequest(
                        'metadata-race-source',
                        'metadata-race-recording',
                        now + timedelta(seconds=2),
                        release_mbid='metadata-race-release',
                    )
                )
                session.commit()
                assert result.library_record_id == 'metadata-race-target'
        finally:
            event.remove(engine, 'before_cursor_execute', commit_provider_revision_before_record_lock)

        assert revision_committed
        with Session(engine) as session:
            inherited = session.scalar(
                select(LibraryMetadataRevisionRecord)
                .where(LibraryMetadataRevisionRecord.library_record_id == 'metadata-race-target')
                .where(LibraryMetadataRevisionRecord.actor == 'reassociation')
            )
            assert inherited is not None
            assert json.loads(inherited.tags_json) == {'TITLE': 'Current provider title'}
        engine.dispose()


@pytest.mark.postgres
def test_manual_and_batch_association_when_records_are_swapped_use_one_lock_order(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime(2026, 9, 24, tzinfo=UTC)
    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        engine = create_engine(database_url)
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            root = SourceRootRecord(
                id='swap-root',
                display_name='swap-root',
                canonical_path='/sources/swap',
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
            record_a = LibraryRecord(
                id='record-a',
                musicbrainz_recording_id='recording-a',
                musicbrainz_release_id='release-a',
                match_state='matched',
                created_at=now,
                updated_at=now,
            )
            record_z = LibraryRecord(
                id='record-z',
                musicbrainz_recording_id='recording-z',
                musicbrainz_release_id='release-z',
                match_state='matched',
                created_at=now,
                updated_at=now,
            )
            session.add_all(
                (
                    root,
                    SourceRecord(
                        id='source-a',
                        source_path='/sources/swap/a.flac',
                        device=1,
                        inode=1,
                        size_bytes=1,
                        sha256='a' * 64,
                        duration_seconds=180,
                        origin='manual',
                        intake_state='present',
                        source_root=root,
                        library_record=record_a,
                    ),
                    SourceRecord(
                        id='source-z',
                        source_path='/sources/swap/z.flac',
                        device=1,
                        inode=2,
                        size_bytes=1,
                        sha256='b' * 64,
                        duration_seconds=180,
                        origin='manual',
                        intake_state='present',
                        source_root=root,
                        library_record=record_z,
                    ),
                )
            )
            session.commit()
        barrier = Barrier(2)

        def associate_manual() -> str:
            with Session(engine) as session:
                session.execute(text("SET LOCAL lock_timeout = '10s'"))
                barrier.wait(timeout=10)
                result = RecordingAssociationService(session).associate_verified_manual(
                    ManualAssociationRequest('source-a', 'recording-z', now, release_mbid='release-z')
                )
                session.commit()
                return result.library_record_id

        def associate_batch() -> str:
            with Session(engine) as session:
                session.execute(text("SET LOCAL lock_timeout = '10s'"))
                barrier.wait(timeout=10)
                result = RecordingAssociationService(session).associate_automatic_batch(
                    (
                        AutomaticAssociationRequest(
                            'source-z', 'recording-a', 1.0, 0.9, '{}', now, release_mbid='release-a'
                        ),
                    )
                )[0]
                session.commit()
                assert result is not None
                return result.library_record_id

        with ThreadPoolExecutor(max_workers=2) as executor:
            manual = executor.submit(associate_manual)
            batch = executor.submit(associate_batch)
            assert manual.result(timeout=20) == 'record-z'
            assert batch.result(timeout=20) == 'record-a'
        engine.dispose()


def _migration_config(database_url: str, legacy_root: Path) -> Config:
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', database_url)
    config.cmd_opts = Namespace(x=[f'legacy_incoming_root={legacy_root}'])
    return config


@pytest.mark.postgres
def test_schema_when_upgraded_on_postgresql_enforces_media_library_contracts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a real PostgreSQL database and a readable legacy child root.
    source_parent = tmp_path / 'sources'
    legacy_root = source_parent / 'legacy'
    legacy_root.mkdir(parents=True)
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')

    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        config = _migration_config(database_url, legacy_root)
        engine = create_engine(database_url)

        # When: the baseline is populated with legacy sources and Todo 1 reaches head.
        command.upgrade(config, _BASE_REVISION)
        observed_at = datetime(2026, 8, 11, tzinfo=UTC)
        in_root = legacy_root / 'in-root.flac'
        in_root.write_bytes(b'legacy')
        with engine.begin() as connection:
            connection.execute(
                text(
                    'INSERT INTO jobs (id, kind, state, created_at) '
                    "VALUES ('legacy-targetless-job', 'reconciliation_scan', 'queued', :created_at)"
                ),
                {'created_at': observed_at},
            )
            connection.execute(
                text(
                    'INSERT INTO webhook_receipts '
                    '(event_fingerprint, provider_name, payload_json, received_at, job_id) '
                    "VALUES (:fingerprint, 'notification', '{}', :created_at, 'legacy-targetless-job')"
                ),
                {'fingerprint': 'f' * 64, 'created_at': observed_at},
            )
            connection.execute(
                text(
                    'INSERT INTO source_records '
                    '(id, source_path, device, inode, size_bytes, sha256, origin, intake_state) '
                    "VALUES (:id, :source_path, 1, 2, 3, :sha256, 'manual', 'present')"
                ),
                {'id': 'source-in-root', 'source_path': str(in_root), 'sha256': 'a' * 64},
            )
            connection.execute(
                text(
                    'INSERT INTO source_records '
                    '(id, source_path, device, inode, size_bytes, sha256, origin, intake_state) '
                    "VALUES ('source-out-root', '/outside/out-root.flac', 1, 3, 4, :sha256, 'manual', 'present')"
                ),
                {'sha256': 'b' * 64},
            )
            connection.execute(
                text(
                    'INSERT INTO source_records '
                    '(id, source_path, device, inode, size_bytes, sha256, origin, intake_state) '
                    "VALUES ('source-missing', :source_path, 1, 4, 5, :sha256, 'manual', 'present')"
                ),
                {'source_path': str(legacy_root / 'missing.flac'), 'sha256': 'c' * 64},
            )
        command.upgrade(config, 'head')

        # Then: the durable schema, history, backfill, and PostgreSQL-only constraints are present.
        inspector = inspect(engine)
        with engine.connect() as connection:
            assert connection.execute(text('SELECT version_num FROM alembic_version')).scalar_one() == _HEAD_REVISION
        assert {
            'source_roots',
            'source_recording_assignments',
            'source_association_overrides',
            'effective_source_decisions',
            'publication_attempts',
        }.issubset(inspector.get_table_names())
        attempt_columns = {column['name'] for column in inspector.get_columns('publication_attempts')}
        assert {
            'library_record_id',
            'source_id',
            'metadata_revision_id',
            'target_directory',
            'target_audio_name',
            'staging_directory',
            'backup_directory',
            'manifest_sha256',
            'output_sha256',
            'created_at',
            'exposed_at',
            'finalized_at',
            'failure_reason',
        }.issubset(attempt_columns)
        with engine.connect() as connection:
            root_rows = connection.execute(text('SELECT id, scan_state FROM source_roots')).mappings().all()
            source_root_rows = (
                connection.execute(text('SELECT id, source_root_id FROM source_records')).mappings().all()
            )
            index_rows = (
                connection.execute(text("SELECT indexname FROM pg_indexes WHERE schemaname = 'public'"))
                .mappings()
                .all()
            )
        roots = {row['id']: row['scan_state'] for row in root_rows}
        source_roots = {row['id']: row['source_root_id'] for row in source_root_rows}
        indexes = {row['indexname'] for row in index_rows}
        assert roots['historical-unmanaged'] == 'never_scanned'
        assert source_roots == {
            'source-in-root': 'historical-unmanaged',
            'source-out-root': 'historical-unmanaged',
            'source-missing': 'historical-unmanaged',
        }
        with engine.connect() as connection:
            historic_job_id = connection.execute(
                text("SELECT id FROM jobs WHERE id = 'legacy-targetless-job'")
            ).scalar_one()
            receipt_job_id = connection.execute(
                text('SELECT job_id FROM webhook_receipts WHERE event_fingerprint = :fingerprint'),
                {'fingerprint': 'f' * 64},
            ).scalar_one()
        assert historic_job_id == 'legacy-targetless-job'
        assert receipt_job_id == 'legacy-targetless-job'
        assert {
            'uq_active_lrclib_fetch_job',
            'uq_active_selection_refresh_job',
            'ix_pending_publication_intent',
            'uq_current_library_publication',
            'ix_candidate_evidence_source_id',
            'ix_candidate_evidence_run_id',
            'ix_provider_candidate_runs_source_id',
            'ix_source_tag_observations_source_id',
        }.issubset(indexes)

        with Session(engine) as session:
            session.execute(
                text(
                    'INSERT INTO library_records '
                    '(id, source_state, processing_state, match_state, publication_state, metadata_state, '
                    'created_at, updated_at) '
                    "VALUES ('record-1', 'present', 'queued', 'unmatched', 'absent', 'original', "
                    ':created_at, :created_at)'
                ),
                {'created_at': observed_at},
            )
            session.execute(
                text(
                    'INSERT INTO jobs (id, source_id, kind, state, created_at) '
                    "VALUES ('job-source', 'source-in-root', 'filesystem_scan', 'queued', :created_at)"
                ),
                {'created_at': observed_at},
            )
            session.commit()
            with pytest.raises(IntegrityError):
                session.execute(
                    text(
                        'INSERT INTO jobs (id, source_id, library_record_id, kind, state, created_at) '
                        "VALUES ('job-dual-target', 'source-in-root', 'record-1', 'selection_refresh', "
                        "'queued', :created_at)"
                    ),
                    {'created_at': observed_at},
                )
                session.commit()
            session.rollback()
            with pytest.raises(IntegrityError):
                session.execute(
                    text(
                        'INSERT INTO source_roots (id, display_name, canonical_path, enabled, scan_state, '
                        'created_at, updated_at) '
                        "VALUES ('duplicate-legacy', 'duplicate', :path, true, 'never_scanned', "
                        ':created_at, :created_at)'
                    ),
                    {'path': 'historical-unmanaged://', 'created_at': observed_at},
                )
                session.commit()
            session.rollback()
            session.execute(
                text(
                    'INSERT INTO library_publications '
                    '(id, library_record_id, source_id, path, format_name, content_sha256, state, created_at) '
                    "VALUES ('publication-current-1', 'record-1', 'source-in-root', 'recordings/one.flac', "
                    "'flac', :sha256, 'current', :created_at)"
                ),
                {'sha256': 'd' * 64, 'created_at': observed_at},
            )
            session.commit()
            with pytest.raises(IntegrityError):
                session.execute(
                    text(
                        'INSERT INTO library_publications '
                        '(id, library_record_id, source_id, path, format_name, content_sha256, state, created_at) '
                        "VALUES ('publication-current-2', 'record-1', 'source-in-root', 'recordings/two.flac', "
                        "'flac', :sha256, 'current', :created_at)"
                    ),
                    {'sha256': 'e' * 64, 'created_at': observed_at},
                )
                session.commit()
            session.rollback()

        command.downgrade(config, 'base')
        assert 'source_roots' not in inspect(engine).get_table_names()
        engine.dispose()


@pytest.mark.postgres
@pytest.mark.parametrize('legacy_argument', ('', '/not-a-child', '../legacy'))
def test_migration_when_legacy_root_is_invalid_preserves_baseline_rows(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, legacy_argument: str
) -> None:
    # Given: a populated baseline database and a source parent.
    source_parent = tmp_path / 'sources'
    legacy_root = source_parent / 'legacy'
    legacy_root.mkdir(parents=True)
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')

    with PostgresContainer('postgres:17') as postgres:
        database_url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        config = _migration_config(database_url, legacy_root)
        config.cmd_opts = Namespace(x=[] if not legacy_argument else [f'legacy_incoming_root={legacy_argument}'])
        engine = create_engine(database_url)
        command.upgrade(config, _BASE_REVISION)
        with engine.begin() as connection:
            connection.execute(
                text(
                    'INSERT INTO source_records '
                    '(id, source_path, device, inode, size_bytes, sha256, origin, intake_state) '
                    "VALUES ('source-existing', :source_path, 1, 2, 3, :sha256, 'manual', 'present')"
                ),
                {'source_path': str(legacy_root / 'existing.flac'), 'sha256': 'a' * 64},
            )

        # When: migration input is missing, outside the parent, or noncanonical.
        with pytest.raises(RuntimeError):
            command.upgrade(config, 'head')

        # Then: migration validation ran before changing any source evidence or root rows.
        with engine.connect() as connection:
            assert connection.execute(text('SELECT count(*) FROM source_records')).scalar_one() == 1
            assert 'source_roots' not in inspect(engine).get_table_names()
        engine.dispose()
