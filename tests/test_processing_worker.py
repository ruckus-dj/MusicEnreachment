from __future__ import annotations

import json
from dataclasses import replace
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
from music_ingest.matching.providers import AcoustIdFixtureProvider, MusicBrainzFixtureProvider
from music_ingest.persistence.models import Base, JobAttemptRecord, JobRecord, ProviderScheduleRecord, SourceRecord
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
            'sine=frequency=440:duration=10',
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


def _tagless_flac(path: Path) -> Path:
    source = _flac(path)
    removed = run(  # noqa: S603
        ['metaflac', '--remove-all-tags', str(source)],  # noqa: S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert removed.returncode == 0, removed.stderr
    return source


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

    # Then: initial final media is independently published before provider analysis.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-1')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        source = session.get(SourceRecord, job.source_id)
        assert source is not None
        assert source.intake_state == 'present'
        assert source.review_decisions == []
        assert source.library_record is not None
        assert {revision.layer for revision in source.library_record.metadata_revisions} == {'original', 'final'}
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


def test_worker_when_unexpected_processing_error_retries_without_quarantining_source(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a valid source with a queued initial job.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-unexpected', source_id=source.id, kind='analyze', state='queued', created_at=datetime.now(UTC)
            )
        )
        session.commit()

    def fail_unexpected(*_args: object, **_kwargs: object) -> None:
        raise RuntimeError('unexpected processing failure')

    monkeypatch.setattr(ProcessingWorker, '_process', fail_unexpected)

    # When: the worker encounters an exception outside its typed processing errors.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the source stays valid and the job remains retryable.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-unexpected')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'queued'
        assert source is not None and source.intake_state == 'discovered'
        assert source.library_record is not None
        assert source.library_record.events[-1].kind == 'processing_retry'


def test_worker_runs_initial_provider_and_final_publish_phases_in_order(tmp_path: Path) -> None:
    # Given: an import with both providers configured and durable provider schedules.
    config = replace(
        _config(tmp_path),
        musicbrainz_provider=MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz'),
        acoustid_provider=AcoustIdFixtureProvider(Path(__file__).parent / 'fixtures' / 'acoustid'),
    )
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        source_id = source.id
        session.add_all(
            (
                ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=datetime.now(UTC)),
                ProviderScheduleRecord(provider_name='acoustid', next_start_at=datetime.now(UTC)),
                JobRecord(
                    id='job-phases',
                    source_id=source.id,
                    kind='analyze',
                    state='queued',
                    created_at=datetime.now(UTC),
                ),
            )
        )
        session.commit()

    # When: the worker drains initial import, provider analysis, and final publication jobs.
    with Session(engine) as session:
        worker = ProcessingWorker(session, config)
        assert worker.run_once()
        session.commit()
        assert worker.run_once()
        session.commit()
        assert worker.run_once()
        session.commit()

    # Then: the current publication points at the provider-derived final revision and contains its tags.
    with Session(engine) as session:
        jobs = list(session.query(JobRecord).order_by(JobRecord.created_at, JobRecord.id))
        assert [job.kind for job in jobs] == ['analyze', 'provider_analysis', 'final_publish']
        assert [job.state for job in jobs] == ['completed', 'completed', 'completed']
        source = session.get(SourceRecord, source_id)
        assert source is not None and source.library_record is not None
        revisions = {
            (item.layer, item.revision): json.loads(item.tags_json) for item in source.library_record.metadata_revisions
        }
        assert revisions['original', 1]['ALBUM'] == 'Fixture Album'
        assert revisions['analyzed', 1]['MUSICBRAINZ_ALBUMID'] == '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'
        assert 'TITLE' not in revisions['analyzed', 1]
        assert 'GENRE' not in revisions['analyzed', 1]
        assert revisions['final', 2]['TITLE'] == revisions['original', 1]['TITLE']
        assert revisions['final', 2]['ALBUM'] == 'Fixture Release'
        current = next(item for item in source.library_publications if item.state == 'current')
        assert current.metadata_revision_id is not None
        published_path = Path(current.path)
        tags = run(  # noqa: S603
            ['metaflac', '--export-tags-to=-', str(published_path)],  # noqa: S607
            capture_output=True,
            check=False,
            text=True,
            timeout=10,
        )
        assert tags.returncode == 0
        assert 'ALBUM=Fixture Release' in tags.stdout
        assert 'MUSICBRAINZ_ALBUMID=4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c' in tags.stdout


def test_worker_when_valid_source_has_no_canonical_tags_queues_review_without_publishing(tmp_path: Path) -> None:
    # Given: a valid FLAC with no source tags and an immutable queued job.
    config = replace(
        _config(tmp_path),
        acoustid_provider=AcoustIdFixtureProvider(Path(__file__).parent / 'fixtures' / 'acoustid'),
    )
    config.incoming_root.mkdir()
    source_path = _tagless_flac(config.incoming_root / 'tagless.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(ProviderScheduleRecord(provider_name='acoustid', next_start_at=datetime.now(UTC)))
        session.add(
            JobRecord(
                id='job-tagless', source_id=source.id, kind='analyze', state='queued', created_at=datetime.now(UTC)
            )
        )
        session.commit()

    # When: one worker processes the valid source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: the source is reviewable, the job is complete, and audio is published without usable metadata.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-tagless')
        assert job is not None and job.state == 'completed'
        source = session.get(SourceRecord, job.source_id)
        assert source is not None and source.intake_state == 'present'
        assert len(source.library_publications) == 1
        assert source.library_record is not None and source.library_record.processing_state == 'analyzing'
        assert [evidence.state for evidence in source.fingerprints] == ['success']
        assert source.fingerprints[0].fingerprint
        assert source.provider_attempts == []
        assert source.candidates == []
        assert [decision.rationale for decision in source.review_decisions] == []
        revisions = {
            revision.layer: json.loads(revision.tags_json) for revision in source.library_record.metadata_revisions
        }
        assert revisions == {'original': {}, 'final': {}}
    assert list(config.media_root.rglob('*.flac'))


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
        assert not (config.staging_root / 'job-retry').exists()
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


def test_worker_when_source_tags_cannot_form_a_fallback_publishes_observed_tags(tmp_path: Path) -> None:
    # Given: a valid FLAC whose observed tags cannot form a canonical fallback.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    removed = run(  # noqa: S603
        ['metaflac', '--remove-tag=ALBUM', str(source_path)],  # noqa: S607
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

    # Then: the claim is terminal and observed tags are published for later analysis.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-invalid-tags')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        assert source is not None and source.intake_state == 'present'
        assert source.library_record is not None and source.library_record.processing_state == 'needs_review'
        assert source.review_decisions == []
        assert {revision.layer for revision in source.library_record.metadata_revisions} == {'original', 'final'}
        assert len(source.library_publications) == 1
    assert list(config.media_root.rglob('*.flac'))
