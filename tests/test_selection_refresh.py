from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from subprocess import run

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.library.service import append_metadata_revision
from music_ingest.models import (
    Base,
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.normalize.tags import write_normalized_tags
from music_ingest.processing import ProcessingConfig, ProcessingWorker


def _flac(path: Path, title: str) -> Path:
    completed = run(  # noqa: S603
        [  # noqa: S607
            'ffmpeg',
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
    _ = write_normalized_tags(
        path,
        (('TITLE', title), ('ARTIST', 'Fixture Artist'), ('ALBUM', 'Fixture Album'), ('GENRE', 'Rock')),
    )
    return path


def _config(tmp_path: Path) -> ProcessingConfig:
    return ProcessingConfig(
        incoming_root=tmp_path / 'incoming',
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
    )


def test_selection_refresh_when_no_eligible_source_completes_record_target_job_without_output(tmp_path: Path) -> None:
    # Given: a record-target refresh whose policy has no eligible source to publish.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-refresh.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        session.add(LibraryRecord(id='record-empty', created_at=now, updated_at=now))
        assert JobRepository(session).enqueue_selection_refresh('record-empty', now) is not None
        session.commit()

    # When: the real worker claims and dispatches the record-target job.
    with Session(engine) as session:
        worker = ProcessingWorker(session, _config(tmp_path))
        assert worker.run_once()
        session.commit()

        # Then: it finishes without treating the record-target job as a source job or writing output.
        job = session.query(JobRecord).filter_by(library_record_id='record-empty').one()
        assert job.state == 'completed'
        assert not (tmp_path / 'media').exists()


def test_selection_refresh_when_selected_source_disappeared_preserves_current_output(tmp_path: Path) -> None:
    # Given: a record retaining one published output after its only source disappears.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-retention.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    output = tmp_path / 'media' / 'record-retained' / 'audio.flac'
    output.parent.mkdir(parents=True)
    _ = output.write_bytes(b'published-output')
    with Session(engine) as session:
        record = LibraryRecord(id='record-retained', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-disappeared',
            source_path=str(tmp_path / 'missing.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='disappeared',
            disappeared_at=now,
            library_record=record,
        )
        session.add_all(
            (
                record,
                source,
                LibraryPublicationRecord(
                    id='publication-retained',
                    library_record=record,
                    source=source,
                    path=str(output),
                    format_name='flac',
                    content_sha256='b' * 64,
                    state='current',
                    created_at=now,
                ),
            )
        )
        refresh_job = JobRepository(session).enqueue_selection_refresh(record.id, now)
        assert refresh_job is not None
        session.commit()

    # When: the refresh has no eligible replacement to publish.
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: output and current publication are retained with an explainable event.
        publications = session.query(LibraryPublicationRecord).filter_by(library_record_id='record-retained')
        current = publications.filter_by(state='current').one()
        assert current.id == 'publication-retained'
        assert output.read_bytes() == b'published-output'
        assert session.query(PublicationAttemptRecord).count() == 0
        decision = session.get(EffectiveSourceDecisionRecord, 'record-retained')
        assert decision is not None
        assert decision.source_id is None
        assert '"code":"no_eligible_source"' in decision.reason
        event = session.query(LibraryEventRecord).filter_by(library_record_id='record-retained').one()
        assert event.kind == 'selection_refresh_no_eligible_source'


def test_selection_refresh_when_better_source_replaces_output_atomically(tmp_path: Path) -> None:
    # Given: a current publication and a strictly better eligible FLAC source.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    old_source_path = _flac(config.incoming_root / 'old.flac', 'Old Track')
    better_source_path = _flac(config.incoming_root / 'better.flac', 'Better Track')
    better_source_path = better_source_path.resolve()
    better_source_stat = better_source_path.stat()
    better_source_hash = sha256(better_source_path.read_bytes()).hexdigest()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-replace.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-replace', created_at=now, updated_at=now)
        source_root = SourceRootRecord(
            id='better-source-root',
            display_name='Better source root',
            canonical_path=str(config.incoming_root.resolve()),
            enabled=True,
            scan_state='never_scanned',
            created_at=now,
            updated_at=now,
        )
        old_source = SourceRecord(
            id='source-old',
            source_path=str(old_source_path),
            device=old_source_path.stat().st_dev,
            inode=old_source_path.stat().st_ino,
            size_bytes=old_source_path.stat().st_size,
            sha256=sha256(old_source_path.read_bytes()).hexdigest(),
            duration_seconds=1,
            origin='manual',
            source_root=source_root,
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=16,
            media_sample_rate=44_100,
            media_channels=1,
            media_bitrate=0,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='old')],
        )
        better_source = SourceRecord(
            id='source-better',
            source_path=str(better_source_path),
            device=better_source_stat.st_dev,
            inode=better_source_stat.st_ino,
            size_bytes=better_source_stat.st_size,
            sha256=better_source_hash,
            duration_seconds=1,
            origin='manual',
            source_root=source_root,
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=24,
            media_sample_rate=96_000,
            media_channels=2,
            media_bitrate=0,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='better')],
        )
        session.add_all((record, source_root, old_source, better_source))
        session.flush()
        old_revision = append_metadata_revision(
            session, record.id, old_source.id, 'final', {'TITLE': 'Old Track'}, 'test', now
        )
        better_revision = LibraryMetadataRevisionRecord(
            library_record_id=record.id,
            source_id=better_source.id,
            layer='final',
            revision=2,
            tags_json='{"TITLE": "Better Track"}',
            actor='test',
            created_at=now,
        )
        session.add(better_revision)
        session.flush()
        output = config.media_root / 'Fixture Artist' / 'Fixture Album' / '01 - Old Track.flac'
        output.parent.mkdir(parents=True)
        output.write_bytes(old_source_path.read_bytes())
        old_bytes = output.read_bytes()
        session.add(
            LibraryPublicationRecord(
                id='publication-old',
                library_record=record,
                source=old_source,
                path=str(output),
                format_name='flac',
                content_sha256=sha256(old_bytes).hexdigest(),
                metadata_revision_id=old_revision.id,
                state='current',
                created_at=now,
            )
        )
        better_revision_id = better_revision.id
        assert JobRepository(session).enqueue_selection_refresh(record.id, now) is not None
        session.commit()

    # When: the real selection refresh publishes the better source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: one current publication points at the selected source and the old row is immutable history.
        publications = session.query(LibraryPublicationRecord).filter_by(library_record_id='record-replace').all()
        decision = session.get(EffectiveSourceDecisionRecord, 'record-replace')
        assert decision is not None and decision.source_id == 'source-better', decision.reason if decision else None
        job = session.query(JobRecord).filter_by(library_record_id='record-replace').one()
        assert job.state == 'completed', job.failure_reason
        assert session.query(PublicationAttemptRecord).count() == 1
        assert [(item.id, item.state) for item in publications] == [
            ('publication-old', 'superseded'),
            (next(item.id for item in publications if item.id != 'publication-old'), 'current'),
        ]
        assert [item.state for item in publications].count('current') == 1
        current = next(item for item in publications if item.state == 'current')
        previous = next(item for item in publications if item.id == 'publication-old')
        assert previous.state == 'superseded'
        assert output.read_bytes() == old_bytes
        assert current.source_id == 'source-better'
        assert current.metadata_revision_id == better_revision_id
        assert current.path != previous.path
        published = Path(current.path)
        assert current.content_sha256 == sha256(published.read_bytes()).hexdigest()
        refreshed_record = session.get(LibraryRecord, 'record-replace')
        assert refreshed_record is not None
        assert refreshed_record.processing_state == 'complete'
        assert refreshed_record.publication_state == 'current'
        attempt = session.query(PublicationAttemptRecord).one()
        assert attempt.state == 'finalized'
        assert attempt.output_sha256 == current.content_sha256
        assert attempt.manifest_sha256 is not None
        assert not Path(attempt.staging_directory).exists()
        assert not Path(attempt.backup_directory).exists()


def test_selection_refresh_dispatches_persisted_effective_source_without_source_job(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: an eligible effective source with a persisted final metadata revision.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-dispatch.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-selected', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-selected',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=24,
            media_sample_rate=96_000,
            media_channels=2,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((record, source))
        session.flush()
        _ = append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Fixture'}, 'test', now)
        assert JobRepository(session).enqueue_selection_refresh(record.id, now) is not None
        session.commit()

    dispatched_source_ids: list[str] = []

    def publish_selected(worker: ProcessingWorker, claimed: ClaimedJob, timestamp: datetime) -> None:
        _ = worker, timestamp
        assert claimed.job.source_id is not None
        dispatched_source_ids.append(claimed.job.source_id)

    monkeypatch.setattr(ProcessingWorker, '_process_final_publish', publish_selected)
    # When: the record-target worker claim runs.
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: only the persisted effective source is dispatched without a second durable source job.
        assert dispatched_source_ids == ['source-selected']
        assert session.query(JobRecord).filter_by(source_id='source-selected').count() == 0


def test_selection_refresh_recovers_final_metadata_from_reassociated_source(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a selected source whose immutable final revision still belongs to its prior record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-recovery.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        previous = LibraryRecord(id='record-previous', created_at=now, updated_at=now)
        record = LibraryRecord(id='record-recovered', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-recovered',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=24,
            media_sample_rate=96_000,
            media_channels=2,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((previous, record, source))
        session.flush()
        _ = append_metadata_revision(session, previous.id, source.id, 'final', {'TITLE': 'Recovered'}, 'test', now)
        assert JobRepository(session).enqueue_selection_refresh(record.id, now) is not None
        session.commit()

    dispatched_source_ids: list[str] = []

    def publish_selected(worker: ProcessingWorker, claimed: ClaimedJob, timestamp: datetime) -> None:
        _ = worker, timestamp
        assert claimed.job.source_id is not None
        dispatched_source_ids.append(claimed.job.source_id)

    monkeypatch.setattr(ProcessingWorker, '_process_final_publish', publish_selected)
    # When: the real record-target refresh claims the source.
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: final metadata is copied append-only to the target record before publication dispatch.
        assert dispatched_source_ids == ['source-recovered']
        revisions = session.query(LibraryMetadataRevisionRecord).order_by(LibraryMetadataRevisionRecord.id).all()
        assert [(item.library_record_id, item.actor) for item in revisions] == [
            ('record-previous', 'test'),
            ('record-recovered', 'reassociation_recovery'),
        ]
        assert [item.tags_json for item in revisions] == ['{"TITLE": "Recovered"}', '{"TITLE": "Recovered"}']


def test_selection_refresh_when_current_publication_matches_revision_records_no_attempt(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a selected source and final revision already represented by current output.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-refresh-unchanged.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-unchanged', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-unchanged',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=24,
            media_sample_rate=96_000,
            media_channels=2,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((record, source))
        session.flush()
        revision = append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Fixture'}, 'test', now)
        session.add(
            LibraryPublicationRecord(
                id='publication-unchanged',
                library_record=record,
                source=source,
                path=str(tmp_path / 'media' / 'audio.flac'),
                format_name='flac',
                content_sha256='b' * 64,
                metadata_revision_id=revision.id,
                state='current',
                created_at=now,
            )
        )
        JobRepository(session).enqueue_selection_refresh(record.id, now)
        session.commit()

    monkeypatch.setattr(ProcessingWorker, '_process_final_publish', lambda *_args: pytest.fail('unexpected publish'))
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()
        assert session.query(PublicationAttemptRecord).count() == 0


def test_selection_refresh_when_current_publication_extension_differs_dispatches_publish(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: the same selected source and final revision, but a current output with a different extension.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-refresh-extension.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-extension', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-extension',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=24,
            media_sample_rate=96_000,
            media_channels=2,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((record, source))
        session.flush()
        revision = append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Fixture'}, 'test', now)
        session.add(
            LibraryPublicationRecord(
                id='publication-extension',
                library_record=record,
                source=source,
                path=str(tmp_path / 'media' / 'audio.mp3'),
                format_name='mp3',
                content_sha256='b' * 64,
                metadata_revision_id=revision.id,
                state='current',
                created_at=now,
            )
        )
        JobRepository(session).enqueue_selection_refresh(record.id, now)
        session.commit()

    dispatched_source_ids: list[str] = []

    def publish_selected(worker: ProcessingWorker, claimed: ClaimedJob, timestamp: datetime) -> None:
        _ = worker, timestamp
        assert claimed.job.source_id is not None
        dispatched_source_ids.append(claimed.job.source_id)

    monkeypatch.setattr(ProcessingWorker, '_process_final_publish', publish_selected)
    # When: the record-target worker claim runs.
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: the extension difference triggers a publication dispatch.
        assert dispatched_source_ids == ['source-extension']


def test_selection_refresh_when_record_target_is_missing_retries_safely(tmp_path: Path) -> None:
    # Given: a selection refresh job whose record target no longer exists.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "selection-missing.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        assert JobRepository(session).enqueue_selection_refresh('record-missing', now) is not None
        session.commit()

    # When: the worker claims the record-target job.
    with Session(engine) as session:
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: it does not dispatch a source job and records a retryable failure.
        job = session.query(JobRecord).filter_by(library_record_id='record-missing').one()
        assert job.state == 'queued'
        assert job.source_id is None
