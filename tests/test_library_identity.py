from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.persistence.models import (
    Base,
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceRecord,
    SourceTagRecord,
)
from music_ingest.reconciliation import reconcile_incoming


def test_library_record_keeps_multiple_sources_and_publication_history(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "library.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        record = LibraryRecord(
            id='record-1',
            musicbrainz_recording_id='11111111-1111-4111-8111-111111111111',
            created_at=observed_at,
            updated_at=observed_at,
        )
        mp3 = SourceRecord(
            id='source-mp3',
            source_path='/incoming/song.mp3',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        flac = SourceRecord(
            id='source-flac',
            source_path='/incoming/song.flac',
            device=1,
            inode=4,
            size_bytes=5,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        publication = LibraryPublicationRecord(
            id='publication-1',
            library_record=record,
            source=mp3,
            path='Artist/Album/song.flac',
            format_name='flac',
            content_sha256='c' * 64,
            state='current',
            created_at=observed_at,
        )
        session.add_all((record, mp3, flac, publication))
        session.commit()

        persisted = session.get(LibraryRecord, 'record-1')

    assert persisted is not None
    assert persisted.musicbrainz_recording_id == '11111111-1111-4111-8111-111111111111'
    assert {source.id for source in persisted.sources} == {'source-mp3', 'source-flac'}
    assert persisted.publications[0].source_id == 'source-mp3'


def test_library_api_exposes_stable_record_and_file_history(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "library-api.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-api', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-api',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all(
            (
                record,
                source,
                LibraryPublicationRecord(
                    id='publication-api',
                    library_record=record,
                    source=source,
                    path='Artist/song.flac',
                    format_name='flac',
                    content_sha256='b' * 64,
                    state='current',
                    created_at=timestamp,
                ),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))
    response = client.get('/api/library/records/record-api')

    assert response.status_code == 200
    assert response.json()['record_id'] == 'record-api'
    assert response.json()['sources'][0]['source_id'] == 'source-api'
    assert response.json()['publications'][0]['publication_id'] == 'publication-api'

    identity = client.put(
        '/api/library/records/record-api/identity',
        json={'musicbrainz_recording_id': '11111111-1111-4111-8111-111111111111'},
    )

    assert identity.status_code == 200
    assert identity.json()['musicbrainz_recording_id'] == '11111111-1111-4111-8111-111111111111'


def test_library_catalog_sorts_records_by_artist_album_track_and_title(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "library-order.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    records = (
        ('record-b', 'source-b', 'Artist B', 'Album 1', 'Track 10', '10'),
        ('record-a2', 'source-a2', 'Artist A', 'Album 2', 'Track 1', '1'),
        ('record-a1', 'source-a1', 'Artist A', 'Album 1', 'Track 2', '2'),
    )
    with Session(engine) as session:
        for record_id, source_id, artist, album, title, track_number in records:
            record = LibraryRecord(id=record_id, created_at=timestamp, updated_at=timestamp)
            source = SourceRecord(
                id=source_id,
                source_path=f'/incoming/{source_id}.flac',
                device=1,
                inode=len(source_id),
                size_bytes=3,
                sha256=source_id[0] * 64,
                duration_seconds=180,
                origin='manual',
                intake_state='present',
                library_record=record,
            )
            source.tag_observations = [
                SourceTagRecord(format_name='flac', tag_name='ARTIST', value=artist),
                SourceTagRecord(format_name='flac', tag_name='ALBUM', value=album),
                SourceTagRecord(format_name='flac', tag_name='TITLE', value=title),
                SourceTagRecord(format_name='flac', tag_name='TRACKNUMBER', value=track_number),
            ]
            session.add_all((record, source))
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    response = client.get('/api/library/records')

    assert response.status_code == 200
    assert [item['record_id'] for item in response.json()['items']] == ['record-a1', 'record-a2', 'record-b']


def test_provider_retry_api_requeues_failed_and_missing_provider_work_without_duplicate_jobs(tmp_path: Path) -> None:
    # Given: one failed source, one source never sent to a provider, and one successful source.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider-retry.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-retry', created_at=timestamp, updated_at=timestamp)
        failed = SourceRecord(
            id='source-failed',
            source_path='/incoming/failed.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        never_sent = SourceRecord(
            id='source-never',
            source_path='/incoming/never.flac',
            device=1,
            inode=3,
            size_bytes=4,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        successful = SourceRecord(
            id='source-success',
            source_path='/incoming/success.flac',
            device=1,
            inode=4,
            size_bytes=5,
            sha256='c' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all(
            (
                record,
                failed,
                never_sent,
                successful,
                ProviderAttemptRecord(
                    source=failed,
                    provider_name='acoustid',
                    outcome='malformed',
                    snapshot_sha256='d' * 64,
                    snapshot='{}',
                ),
                ProviderAttemptRecord(
                    source=successful,
                    provider_name='acoustid',
                    outcome='acoustidmatch',
                    snapshot_sha256='e' * 64,
                    snapshot='{}',
                ),
                JobRecord(
                    id='job-failed',
                    source_id='source-failed',
                    kind='filesystem_scan',
                    state='completed',
                    created_at=timestamp,
                ),
                JobRecord(
                    id='job-never',
                    source_id='source-never',
                    kind='filesystem_scan',
                    state='completed',
                    created_at=timestamp,
                ),
                JobRecord(
                    id='job-success',
                    source_id='source-success',
                    kind='filesystem_scan',
                    state='completed',
                    created_at=timestamp,
                ),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator retries one failed source and then requests the bulk provider retry.
    single = client.post('/api/library/records/record-retry/sources/source-failed/provider-retry')
    bulk = client.post('/api/library/providers/retry')

    # Then: the single source is queued once, and bulk queues only the never-sent source.
    assert single.status_code == 200
    assert single.json() == {'source_id': 'source-failed', 'queued': True}
    assert bulk.status_code == 200
    assert bulk.json() == {'queued': 1}
    with Session(engine) as session:
        jobs = {job.source_id: job for job in session.query(JobRecord).all()}
        assert jobs['source-failed'].state == 'queued'
        assert jobs['source-failed'].kind == 'provider_analysis'
        assert jobs['source-never'].state == 'queued'
        assert jobs['source-success'].state == 'completed'
        assert len(jobs) == 3

    with Session(engine) as session:
        source = session.get(SourceRecord, 'source-success')
        assert source is not None
        source.disappeared_at = timestamp
        session.commit()
    disappeared = client.post('/api/library/records/record-retry/sources/source-success/provider-retry')
    assert disappeared.status_code == 200
    assert disappeared.json() == {'source_id': 'source-success', 'queued': False}


def test_reconciliation_replaces_source_version_without_replacing_record(tmp_path: Path) -> None:
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    source_path = incoming / 'song.flac'
    source_path.write_bytes(b'new-source')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "replacement.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-replaced', created_at=timestamp, updated_at=timestamp)
        session.add(
            SourceRecord(
                id='source-old',
                source_path=str(source_path),
                device=1,
                inode=2,
                size_bytes=3,
                sha256='a' * 64,
                duration_seconds=180,
                origin='manual',
                intake_state='present',
                library_record=record,
            )
        )
        session.commit()

        result = reconcile_incoming(session, incoming)
        session.commit()
        session.expire_all()
        persisted = session.get(LibraryRecord, 'record-replaced')

    assert result.changed == 1
    assert persisted is not None
    assert len(persisted.sources) == 2
    assert {source.library_record_id for source in persisted.sources} == {'record-replaced'}
    assert any(source.intake_state == 'replaced' for source in persisted.sources)
