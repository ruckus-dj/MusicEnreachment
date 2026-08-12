from __future__ import annotations

from argparse import Namespace
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from pathlib import Path
from threading import Barrier

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, inspect, text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from alembic import command
from music_ingest.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.models import (
    Base,
    CandidateRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.models.jobs import JobRepository
from music_ingest.processing import ProcessingConfig, ProcessingWorker

_MIGRATION_DIRECTORY = Path(__file__).parents[1] / 'alembic'
_BASE_REVISION = '20260810_0002'
_HEAD_REVISION = '20260812_0004'


@pytest.mark.live
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


@pytest.mark.live
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


@pytest.mark.live
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


@pytest.mark.live
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
                result = RecordingAssociationService(session).associate_automatic(
                    AutomaticAssociationRequest(source_id, recording_mbid, 0.98, 0.9, evidence, now)
                )
                session.commit()
                assert result is not None
                return result.library_record_id

        # When: both transactions create or locate the same recording aggregate simultaneously.
        with ThreadPoolExecutor(max_workers=2) as executor:
            resolved = tuple(executor.map(associate, ('source-1', 'source-2')))

        # Then: the unique MBID record is shared and neither transaction leaks IntegrityError.
        with Session(engine) as session:
            records = session.query(LibraryRecord).filter_by(musicbrainz_recording_id=recording_mbid).all()
            sources = session.query(SourceRecord).filter(SourceRecord.id.in_(('source-1', 'source-2'))).all()
            assert len(records) == 1
            assert set(resolved) == {records[0].id}
            assert {source.library_record_id for source in sources} == {records[0].id}
        engine.dispose()


def _migration_config(database_url: str, legacy_root: Path) -> Config:
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', database_url)
    config.cmd_opts = Namespace(x=[f'legacy_incoming_root={legacy_root}'])
    return config


@pytest.mark.live
def test_schema_when_upgraded_on_postgresql_enforces_media_library_contracts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a real PostgreSQL database and a readable legacy child root.
    source_parent = tmp_path / 'sources'
    legacy_root = source_parent / 'legacy'
    legacy_root.mkdir(parents=True)
    monkeypatch.setenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', str(source_parent))
    monkeypatch.setenv('MUSIC_INGEST_MEDIA_ROOT', str(tmp_path / 'media'))
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
                    "VALUES ('legacy-targetless-job', 'lidarr_albumdelete', 'queued', :created_at)"
                ),
                {'created_at': observed_at},
            )
            connection.execute(
                text(
                    'INSERT INTO webhook_receipts '
                    '(event_fingerprint, provider_name, payload_json, received_at, job_id) '
                    "VALUES (:fingerprint, 'lidarr', '{}', :created_at, 'legacy-targetless-job')"
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
        assert roots['legacy'] == 'never_scanned'
        assert roots['historical-unmanaged'] == 'never_scanned'
        assert source_roots == {
            'source-in-root': 'legacy',
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
        assert {'uq_active_selection_refresh_job', 'uq_current_library_publication'}.issubset(indexes)

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
                    {'path': str(legacy_root), 'created_at': observed_at},
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


@pytest.mark.live
@pytest.mark.parametrize('legacy_argument', ('', '/not-a-child', '../legacy'))
def test_migration_when_legacy_root_is_invalid_preserves_baseline_rows(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, legacy_argument: str
) -> None:
    # Given: a populated baseline database and a source parent.
    source_parent = tmp_path / 'sources'
    legacy_root = source_parent / 'legacy'
    legacy_root.mkdir(parents=True)
    monkeypatch.setenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', str(source_parent))
    monkeypatch.setenv('MUSIC_INGEST_MEDIA_ROOT', str(tmp_path / 'media'))
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
