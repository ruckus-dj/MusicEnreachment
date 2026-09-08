from __future__ import annotations

import os
import shutil
from argparse import Namespace
from collections.abc import Iterator
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from uuid import uuid4

import pytest
from alembic.config import Config
from sqlalchemy import Engine, create_engine, select
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from alembic import command
from music_ingest.library.service import append_metadata_revision
from music_ingest.models import (
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.models.jobs import JobRepository
from music_ingest.normalize.tags import write_normalized_tags
from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.processing.metadata import publication_layout
from music_ingest.publication import attempts
from tests.test_selection_refresh import _flac

pytestmark = pytest.mark.live


@pytest.fixture
def integrity_engine(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[Engine]:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    legacy = tmp_path / 'legacy'
    legacy.mkdir()
    monkeypatch.setenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', str(tmp_path))
    monkeypatch.setenv('MUSIC_INGEST_MEDIA_ROOT', str(tmp_path / 'media'))
    with PostgresContainer('postgres:17') as postgres:
        url = postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg')
        config = Config()
        config.set_main_option('script_location', str(Path(__file__).parents[1] / 'alembic'))
        config.set_main_option('sqlalchemy.url', url)
        config.cmd_opts = Namespace(x=[f'legacy_incoming_root={legacy}'])
        command.upgrade(config, 'head')
        engine = create_engine(url)
        try:
            yield engine
        finally:
            engine.dispose()


def seed_publication(engine: Engine, root: Path) -> tuple[ProcessingConfig, Path, Path]:
    config = ProcessingConfig(root / 'incoming', root / 'processing', root / 'media')
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'source.flac', 'Track').resolve()
    tags = {'TITLE': 'Track', 'ARTIST': 'Artist', 'ALBUM': 'Album'}
    directory, name = publication_layout(tuple(tags.items()), source_path.name)
    target = config.media_root / directory / name
    target.parent.mkdir(parents=True)
    target.write_bytes(b'old-output')
    (target.parent / 'album.nfo').write_bytes(b'preserve nfo')
    now = datetime.now(UTC)
    stat = source_path.stat()
    with Session(engine) as session:
        record = LibraryRecord(id='record', created_at=now, updated_at=now)
        source_root = SourceRootRecord(
            id='root',
            display_name='Root',
            canonical_path=str(source_path.parent),
            enabled=True,
            scan_state='never_scanned',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source',
            source_path=str(source_path),
            device=stat.st_dev,
            inode=stat.st_ino,
            size_bytes=stat.st_size,
            sha256=sha256(source_path.read_bytes()).hexdigest(),
            origin='manual',
            intake_state='present',
            library_record=record,
            source_root=source_root,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((record, source_root, source))
        session.flush()
        revision = append_metadata_revision(session, record.id, source.id, 'final', tags, 'test', now)
        session.add(
            LibraryPublicationRecord(
                id='old',
                library_record_id=record.id,
                source_id=source.id,
                path=str(target),
                format_name='mka',
                content_sha256=sha256(b'old-output').hexdigest(),
                state='current',
                created_at=now,
            )
        )
        JobRepository(session).enqueue(source.id, 'final_publish', now, revision.id)
        session.commit()
    return config, source_path, target


@pytest.mark.parametrize('job_kind', ['final_publish', 'filesystem_scan'])
@pytest.mark.parametrize(
    'checkpoint', ['prepared', 'prepared-restart', 'backup-rename', 'after-expose', 'exposed', 'finalized', 'cleanup']
)
def test_real_worker_recovers_publication_commit_and_restart_failures(
    integrity_engine: Engine,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    checkpoint: str,
    job_kind: str,
) -> None:
    config, source, target = seed_publication(integrity_engine, tmp_path)
    if job_kind == 'filesystem_scan':
        write_normalized_tags(
            source,
            (
                ('TITLE', 'Track'),
                ('ARTIST', 'Artist'),
                ('ALBUM', 'Album'),
                ('MUSICBRAINZ_RECORDINGID', 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'),
                ('MUSICBRAINZ_ALBUMID', '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'),
            ),
        )
        with Session(integrity_engine) as session:
            job = session.scalars(select(JobRecord)).one()
            job.kind = job_kind
            job.metadata_revision_id = None
            observed = session.get(SourceRecord, 'source')
            assert observed is not None
            observed.size_bytes = source.stat().st_size
            observed.sha256 = sha256(source.read_bytes()).hexdigest()
            session.commit()
    original_source = source.read_bytes()
    with Session(integrity_engine) as session:
        commit = session.commit
        expose = attempts.expose_attempt
        cleanup = attempts.cleanup_attempt
        replace = os.replace

        def fail_commit() -> None:
            attempt = session.scalar(select(PublicationAttemptRecord))
            if attempt is not None and attempt.state == checkpoint:
                raise OSError('injected commit failure')
            prepared = attempt is not None and attempt.state == 'prepared'
            commit()
            if checkpoint == 'prepared-restart' and prepared:
                raise OSError('injected restart after prepared commit')

        def fail_replace(src: Path, dst: Path) -> None:
            replace(src, dst)
            if src == target:
                raise OSError('injected restart after backup rename')

        def fail_after_expose(db: Session, attempt: PublicationAttemptRecord, now: datetime) -> None:
            expose(db, attempt, now)
            raise OSError('injected after expose')

        def fail_cleanup(attempt: PublicationAttemptRecord) -> None:
            raise OSError('injected cleanup failure')

        monkeypatch.setattr(session, 'commit', fail_commit)
        if checkpoint == 'backup-rename':
            monkeypatch.setattr(attempts.os, 'replace', fail_replace)
        if checkpoint == 'after-expose':
            monkeypatch.setattr(attempts, 'expose_attempt', fail_after_expose)
        if checkpoint == 'cleanup':
            monkeypatch.setattr(attempts, 'cleanup_attempt', fail_cleanup)
        with pytest.raises(OSError, match='injected'):
            ProcessingWorker(session, config).run_once()
        session.rollback()
        monkeypatch.setattr(attempts.os, 'replace', replace)
        monkeypatch.setattr(attempts, 'expose_attempt', expose)
        monkeypatch.setattr(attempts, 'cleanup_attempt', cleanup)
        durable = session.scalar(select(PublicationAttemptRecord))
        if checkpoint == 'prepared':
            assert durable is None
            assert target.read_bytes() == b'old-output'
        elif checkpoint == 'prepared-restart':
            assert durable is not None and durable.state == 'prepared'
            assert target.read_bytes() == b'old-output'
        else:
            assert durable is not None
            assert (Path(durable.backup_directory) / target.name).read_bytes() == b'old-output'

    # Processing scratch is disposable even while a publication needs recovery.
    shutil.rmtree(config.staging_root, ignore_errors=True)

    # A new worker/session is the recovery entry point, including an empty queue.
    with Session(integrity_engine) as session:
        ProcessingWorker(session, config).run_once()
        session.commit()
        current = session.scalars(
            select(LibraryPublicationRecord).where(LibraryPublicationRecord.state == 'current')
        ).one()
        assert current.id != 'old'
        assert current.content_sha256 == sha256(target.read_bytes()).hexdigest()
        attempt = session.scalars(select(PublicationAttemptRecord)).one()
        assert attempt.state == 'finalized' and attempt.cleaned_at is not None
        assert not Path(attempt.backup_directory).exists()
        assert (target.parent / 'album.nfo').read_bytes() == b'preserve nfo'
        assert source.read_bytes() == original_source


def test_storage_request_and_publication_share_lock(
    integrity_engine: Engine,
    tmp_path: Path,
) -> None:
    from concurrent.futures import ThreadPoolExecutor
    from threading import Event

    from music_ingest.storage import StorageService, StorageValidationError
    from tests.test_storage_migration import finish_migration

    config, source, target = seed_publication(integrity_engine, tmp_path)
    original = source.read_bytes()
    destination = tmp_path / 'new'
    destination.mkdir()
    claimed, release, requested, finished = Event(), Event(), Event(), Event()

    def publish() -> None:
        def pause(job_id: str, kind: str) -> None:
            claimed.set()
            assert release.wait(10)

        with Session(integrity_engine) as session:
            assert ProcessingWorker(session, config).run_once(on_claimed=pause)
            session.commit()

    def move() -> None:
        requested.set()
        try:
            with Session(integrity_engine) as session:
                try:
                    StorageService(session, (tmp_path,), config.media_root).move_output(str(destination))
                    session.commit()
                except StorageValidationError:
                    session.rollback()
        finally:
            finished.set()

    with ThreadPoolExecutor(max_workers=2) as pool:
        publishing = pool.submit(publish)
        assert claimed.wait(10)
        moving = pool.submit(move)
        assert requested.wait(5)
        try:
            assert not finished.wait(0.2), 'migration must wait for the publication transaction'
        finally:
            release.set()
        publishing.result(timeout=15)
        moving.result(timeout=15)
    with Session(integrity_engine) as session:
        StorageService(session, (tmp_path,), config.media_root).move_output(str(destination))
        session.commit()
    finish_migration(integrity_engine)
    with Session(integrity_engine) as session:
        publication = session.scalars(
            select(LibraryPublicationRecord).where(LibraryPublicationRecord.state == 'current')
        ).one()
        assert Path(publication.path).is_relative_to(destination)
        assert sha256(Path(publication.path).read_bytes()).hexdigest() == publication.content_sha256
    assert not target.exists()
    assert source.read_bytes() == original
    assert (target.parent / 'album.nfo').read_bytes() == b'preserve nfo'


def test_two_workers_resume_storage_manifest_once(integrity_engine: Engine, tmp_path: Path) -> None:
    from concurrent.futures import ThreadPoolExecutor

    from music_ingest.models import StorageConfigRecord
    from music_ingest.storage import StorageService
    from tests.test_storage_migration import seed_storage

    old, new = seed_storage(integrity_engine, tmp_path)
    with Session(integrity_engine) as session:
        StorageService(session, (tmp_path,), old).move_output(str(new))
        session.commit()
    config = ProcessingConfig(tmp_path / 'incoming', tmp_path / 'processing', old)

    def work() -> None:
        for _ in range(12):
            with Session(integrity_engine) as session:
                ProcessingWorker(session, config).run_once()
                session.commit()

    with ThreadPoolExecutor(max_workers=2) as pool:
        first, second = pool.submit(work), pool.submit(work)
        first.result(timeout=15)
        second.result(timeout=15)
    with Session(integrity_engine) as session:
        storage = session.get(StorageConfigRecord, 1)
        assert storage is not None and storage.state == 'ready' and storage.generation == 2
        for publication in session.scalars(select(LibraryPublicationRecord)).all():
            assert sha256(Path(publication.path).read_bytes()).hexdigest() == publication.content_sha256


def test_publication_on_separate_processing_and_media_mounts(monkeypatch: pytest.MonkeyPatch) -> None:
    from testcontainers.core.container import DockerContainer
    from testcontainers.core.network import Network

    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    image = os.environ.get('MUSIC_INGEST_INTEGRITY_IMAGE', 'music-enrichment-test-stand-music-ingest:latest')
    with Network() as network:
        credential = uuid4().hex
        postgres = PostgresContainer('postgres:17', username='integrity', password=credential, dbname='integrity')
        postgres.with_network(network).with_network_aliases('integrity-db')
        with postgres:
            runner = DockerContainer(image).with_network(network)
            runner.with_volume_mapping(Path(__file__).parents[1], '/work', 'ro')
            runner.with_kwargs(working_dir='/work')
            runner.with_env('PYTHONPATH', '/work/src')
            runner.with_env(
                'MUSIC_INGEST_DATABASE_URL', f'postgresql+psycopg://integrity:{credential}@integrity-db/integrity'
            )
            runner.with_env('MUSIC_INGEST_MEDIA_ROOT', '/media')
            runner.with_env('MUSIC_INGEST_SOURCE_ROOTS_PARENT', '/sources')
            runner.with_tmpfs_mount('/processing', 'rw,size=64m')
            runner.with_tmpfs_mount('/media', 'rw,size=64m')
            runner.with_tmpfs_mount('/sources', 'rw,size=16m')
            runner.with_command(['sleep', 'infinity'])
            with runner:
                result = runner.exec(['python', '/work/tests/integration/publication_mounts.py'])
                assert result.exit_code == 0, result.output.decode()
                assert b'same-target replacement verified' in result.output
