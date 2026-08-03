from __future__ import annotations

from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path
from shutil import which
from subprocess import run
from typing import Final

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

import music_ingest.processing.worker as processing
from music_ingest.persistence.models import Base, JobAttemptRecord, JobRecord, SourceRecord
from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.publication.service import PublicationError

_FFMPEG: Final[str] = which('ffmpeg') or ''
assert _FFMPEG


def _flac(path: Path) -> Path:
    completed = run(  # noqa: S603
        [
            _FFMPEG,
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-c:a',
            'flac',
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    tagged = run(  # noqa: S603
        [  # noqa: S607
            'metaflac',
            '--set-tag=TITLE=Fixture Track',
            '--set-tag=ARTIST=Fixture Artist; Fixture Guest',
            '--set-tag=ALBUM=Fixture Album',
            '--set-tag=ALBUMARTIST=Fixture Artist; Fixture Guest',
            '--set-tag=DATE=2026',
            '--set-tag=TRACKNUMBER=1',
            '--set-tag=TRACKTOTAL=1',
            '--set-tag=DISCNUMBER=1',
            '--set-tag=DISCTOTAL=1',
            '--set-tag=GENRE=Hip Hop; Alternative Rock',
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert tagged.returncode == 0, tagged.stderr
    return path


def _source(session: Session, path: Path) -> SourceRecord:
    stat = path.stat()
    source = SourceRecord(
        id=sha256(f'{stat.st_dev}:{stat.st_ino}'.encode()).hexdigest(),
        source_path=str(path),
        device=stat.st_dev,
        inode=stat.st_ino,
        size_bytes=stat.st_size,
        sha256=sha256(path.read_bytes()).hexdigest(),
        duration_seconds=1,
        origin='lidarr',
        intake_state='discovered',
    )
    session.add(source)
    session.flush()
    return source


def _config(tmp_path: Path) -> ProcessingConfig:
    return ProcessingConfig(
        incoming_root=tmp_path / 'incoming',
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
        retention_root=tmp_path / 'retention',
        quarantine_root=tmp_path / 'quarantine',
        flac_command='flac',
        metaflac_command='metaflac',
    )


def test_worker_when_valid_source_has_no_provider_match_publishes_original_fallback_and_review(tmp_path: Path) -> None:
    # Given: a DB-backed queued job with a valid immutable incoming FLAC and artwork.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    source_identity = source_path.stat().st_dev, source_path.stat().st_ino, sha256(source_path.read_bytes()).hexdigest()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(id='job-1', source_id=source.id, kind='analyze', state='queued', created_at=datetime.now(UTC))
        )
        session.commit()

    # When: one worker claims and processes the job.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: fallback media is independently published while the source and review evidence remain immutable.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-1')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        source = session.get(SourceRecord, job.source_id)
        assert source is not None
        assert source.intake_state == 'needs_review'
    published = next(config.media_root.rglob('*.flac'))
    assert source_identity == (
        source_path.stat().st_dev,
        source_path.stat().st_ino,
        sha256(source_path.read_bytes()).hexdigest(),
    )
    assert published.stat().st_ino != source_path.stat().st_ino
    tags = run(  # noqa: S603
        ['metaflac', '--export-tags-to=-', str(published)],  # noqa: S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert tags.returncode == 0
    assert 'TITLE=Fixture Track' in tags.stdout
    assert 'GENRE=Hip Hop; Alternative Rock' in tags.stdout


def test_worker_when_media_root_is_a_symlink_keeps_the_published_job_succeeded(tmp_path: Path) -> None:
    # Given: a lexical media root that resolves to the physical publication root.
    config = _config(tmp_path)
    physical_media_root = tmp_path / 'physical-media'
    physical_media_root.mkdir()
    config.media_root.symlink_to(physical_media_root, target_is_directory=True)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-symlink-root', source_id=source.id, kind='analyze', state='queued', created_at=datetime.now(UTC)
            )
        )
        session.commit()

    # When: publication returns its canonical physical release path.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the path is persisted relative to the canonical root and the job remains successful.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-symlink-root')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']


def test_worker_when_interrupted_job_is_stale_reclaims_it_with_a_new_attempt(tmp_path: Path) -> None:
    # Given: a persisted job abandoned by a prior worker.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        job = JobRecord(
            id='job-1',
            source_id=source.id,
            kind='analyze',
            state='running',
            created_at=datetime.now(UTC) - timedelta(minutes=10),
        )
        job.attempts = [
            JobAttemptRecord(
                attempt_number=1,
                state='running',
                started_at=datetime.now(UTC) - timedelta(minutes=10),
                finished_at=None,
            )
        ]
        session.add(job)
        session.commit()

    # When: a restarted worker claims jobs older than its lease.
    with Session(engine) as session:
        assert ProcessingWorker(session, config, lease_age=timedelta(seconds=1)).run_once()
        session.commit()

    # Then: the abandoned attempt is retained and the retry reaches a terminal state.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-1')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['interrupted', 'succeeded']


def test_worker_when_publication_is_transient_waits_before_reclaiming_then_succeeds(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a queued source whose first publication attempt has a transient failure.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(id='job-retry', source_id=source.id, kind='analyze', state='queued', created_at=datetime.now(UTC))
        )
        session.commit()
    publish = processing.publish_release

    def transient_failure(*_args: object, **_kwargs: object) -> None:
        raise PublicationError('temporary publish failure')

    monkeypatch.setattr(processing, 'publish_release', transient_failure)

    # When: the worker records the transient failure, then polls before the retry deadline.
    with Session(engine) as session:
        worker = ProcessingWorker(session, config)
        assert worker.run_once()
        session.commit()
        assert not worker.run_once()
        job = session.get(JobRecord, 'job-retry')
        assert job is not None and job.next_attempt_at is not None
        job.next_attempt_at = datetime.now(UTC) - timedelta(seconds=1)
        session.commit()

    # Then: a due retry reclaims the source and reaches success.
    monkeypatch.setattr(processing, 'publish_release', publish)
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()
        job = session.get(JobRecord, 'job-retry')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['retry_wait', 'succeeded']


def test_worker_when_source_tags_cannot_form_a_fallback_quarantines_the_claimed_job(tmp_path: Path) -> None:
    # Given: a valid FLAC whose observed tags cannot form a canonical fallback.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    removed = run(  # noqa: S603
        ['metaflac', '--remove-tag=GENRE', str(source_path)],  # noqa: S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert removed.returncode == 0, removed.stderr
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-invalid-tags',
                source_id=source.id,
                kind='analyze',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: the worker processes the structurally unusable fallback source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the claim is terminal and the diagnostic is retained outside the source tree.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-invalid-tags')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'quarantined'
        assert [attempt.state for attempt in job.attempts] == ['quarantined']
        assert source is not None and source.intake_state == 'quarantined'
    assert (config.quarantine_root / f'{source.id}.json').is_file()
