from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.contracts import PublicationReconciliationJobResponse
from music_ingest.models import (
    Base,
    JobRecord,
    LibraryEventRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReleaseArtworkRecord,
    SourceRecord,
    StorageConfigRecord,
)
from music_ingest.services.publication.reconciliation import reconcile_publication_directory
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker


def test_publication_reconciliation_removes_only_unrepresented_managed_entries(tmp_path: Path) -> None:
    # Given: represented publication assets, an in-flight workspace, an NFO, and orphaned output.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "cleanup.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    album = media_root / 'Artist' / 'Album'
    album.mkdir(parents=True)
    current_audio = album / '01 - Current.mka'
    historical_audio = album / '02 - Historical.mka'
    artwork = album / 'cover.jpg'
    lyrics = album / '01 - Current.lrc'
    nfo = album / 'album.nfo'
    for path in (current_audio, historical_audio, artwork, lyrics, nfo):
        _ = path.write_bytes(path.name.encode())
    orphan_directory = media_root / 'Orphan'
    orphan_directory.mkdir()
    orphan = orphan_directory / 'junk.tmp'
    _ = orphan.write_bytes(b'junk')
    workspace = media_root / '.music-ingest-publications' / 'attempt'
    staging = workspace / 'staged'
    backup = workspace / 'backup'
    staging.mkdir(parents=True)
    backup.mkdir()
    staged_audio = staging / '03 - Pending.mka'
    _ = staged_audio.write_bytes(b'pending')

    with Session(engine) as session:
        record, source = _record_and_source('kept', tmp_path, now)
        record.lyrics_path = str(lyrics)
        session.add_all(
            (
                record,
                source,
                _storage(media_root, now),
                LibraryPublicationRecord(
                    id='current',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(current_audio),
                    format_name='mka',
                    content_sha256='a' * 64,
                    state='current',
                    created_at=now,
                ),
                LibraryPublicationRecord(
                    id='historical',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(historical_audio),
                    format_name='mka',
                    content_sha256='b' * 64,
                    state='superseded',
                    created_at=now,
                ),
                ReleaseArtworkRecord(
                    release_mbid='release',
                    path=str(artwork),
                    format_name='jpeg',
                    provider='fixture',
                    state='current',
                    created_at=now,
                    updated_at=now,
                ),
                PublicationAttemptRecord(
                    id='attempt',
                    library_record_id=record.id,
                    source_id=source.id,
                    state='prepared',
                    target_directory=str(album),
                    target_audio_name='03 - Pending.mka',
                    staging_directory=str(staging),
                    backup_directory=str(backup),
                    created_at=now,
                    completion_state='complete',
                ),
            )
        )
        session.flush()

        # When: the operator reconciles the publication directory.
        result = reconcile_publication_directory(session, now)
        session.commit()

        # Then: only the orphan is removed; all DB-owned, in-flight, and NFO files remain.
        assert result.removed_files == 1
        assert result.removed_directories == 1
        assert result.preserved_nfo == 1
        assert not orphan.exists()
        assert not orphan_directory.exists()
        assert all(path.exists() for path in (current_audio, historical_audio, artwork, lyrics, nfo, staged_audio))


def test_publication_reconciliation_removes_managed_sidecars_when_no_tracks_remain(tmp_path: Path) -> None:
    # Given: persisted artwork and lyrics point into a release directory without any published tracks.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "orphan-sidecars.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 25, tzinfo=UTC)
    media_root = tmp_path / 'media'
    album = media_root / 'Artist' / 'Empty Album'
    album.mkdir(parents=True)
    artwork = album / 'cover.jpg'
    lyrics = album / '01 - Missing.lrc'
    _ = artwork.write_bytes(b'artwork')
    _ = lyrics.write_text('[00:00.00]Missing', encoding='utf-8')

    with Session(engine) as session:
        record = LibraryRecord(id='record-missing-track', created_at=now, updated_at=now)
        record.lyrics_path = str(lyrics)
        artwork_record = ReleaseArtworkRecord(
            release_mbid='empty-release',
            path=str(artwork),
            format_name='jpeg',
            provider='fixture',
            state='current',
            created_at=now,
            updated_at=now,
        )
        session.add_all((record, _storage(media_root, now), artwork_record))
        session.flush()

        # When: the operator reconciles the publication directory.
        result = reconcile_publication_directory(session, now)

        # Then: orphaned managed sidecars are removed before their empty release and artist directories.
        assert result.removed_files == 2
        assert result.removed_directories == 2
        assert not album.parent.exists()


def test_publication_reconciliation_marks_missing_current_output_and_queues_refresh_once(tmp_path: Path) -> None:
    # Given: a current publication row whose managed audio is absent.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "missing.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    media_root.mkdir()
    missing = media_root / 'Artist' / 'Album' / '01 - Missing.mka'
    with Session(engine) as session:
        record, source = _record_and_source('missing', tmp_path, now)
        session.add_all(
            (
                record,
                source,
                _storage(media_root, now),
                LibraryPublicationRecord(
                    id='missing-publication',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(missing),
                    format_name='mka',
                    content_sha256='a' * 64,
                    state='current',
                    created_at=now,
                ),
            )
        )
        session.flush()

        # When: reconciliation runs twice.
        first = reconcile_publication_directory(session, now)
        session.commit()
        second = reconcile_publication_directory(session, now)
        session.commit()

        # Then: the publication is inactive, the record is failed, and one refresh/event is durable.
        publication = session.get(LibraryPublicationRecord, 'missing-publication')
        refreshed_record = session.get(LibraryRecord, record.id)
        jobs = session.scalars(select(JobRecord).where(JobRecord.kind == 'selection_refresh')).all()
        events = session.scalars(
            select(LibraryEventRecord).where(LibraryEventRecord.kind == 'publication_file_missing')
        ).all()
        assert publication is not None and publication.state == 'inactive'
        assert refreshed_record is not None and refreshed_record.publication_state == 'failed'
        assert first.missing_publications == 1
        assert first.queued_jobs == 1
        assert second.missing_publications == 0
        assert len(jobs) == 1
        assert len(events) == 1


def test_publication_reconciliation_api_coalesces_and_reports_worker_result(tmp_path: Path) -> None:
    # Given: ready publication storage and an empty global job queue.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "api.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    media_root.mkdir()
    with Session(engine) as session:
        session.add(_storage(media_root, now))
        session.commit()
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the action is requested twice and its job is processed.
    first = client.post('/api/reconciliation/publications')
    second = client.post('/api/reconciliation/publications')
    job_id = PublicationReconciliationJobResponse.model_validate_json(first.content).job_id
    with Session(engine) as session:
        worker = ProcessingWorker(
            session,
            ProcessingConfig(
                incoming_root=tmp_path / 'incoming',
                staging_root=tmp_path / 'staging',
                media_root=media_root,
            ),
        )
        assert worker.run_once(allowed_kinds={'publication_reconciliation'})
        session.commit()

    # Then: both requests share one job and the typed terminal result is readable.
    assert first.status_code == 202
    assert second.status_code == 202
    assert second.json()['job_id'] == job_id
    status = client.get(f'/api/reconciliation/publications/{job_id}')
    assert status.status_code == 200
    assert status.json() == {
        'job_id': job_id,
        'state': 'completed',
        'result': {
            'removed_files': 0,
            'removed_directories': 0,
            'preserved_nfo': 0,
            'missing_publications': 0,
            'queued_jobs': 0,
            'already_queued': 0,
            'deferred_publications': 0,
            'unsafe_entries': 0,
        },
    }


def _storage(media_root: Path, now: datetime) -> StorageConfigRecord:
    return StorageConfigRecord(
        id=1,
        output_root=str(media_root),
        migration_json=None,
        state='ready',
        generation=1,
        updated_at=now,
    )


def _record_and_source(prefix: str, root: Path, now: datetime) -> tuple[LibraryRecord, SourceRecord]:
    record = LibraryRecord(id=f'record-{prefix}', created_at=now, updated_at=now)
    source = SourceRecord(
        id=f'source-{prefix}',
        source_path=str(root / f'{prefix}.flac'),
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        library_record=record,
    )
    return record, source
