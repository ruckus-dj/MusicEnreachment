from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import CandidateEvidencePayload, _candidate_is_displayable, create_app
from music_ingest.matching.providers import MusicBrainzFixtureProvider
from music_ingest.persistence.models import (
    Base,
    CandidateRecord,
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    ProviderScheduleRecord,
    ProviderSnapshotRecord,
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


def test_library_api_confirms_provider_candidate_into_final_publication_job(tmp_path: Path) -> None:
    # Given: a source with a stored MusicBrainz candidate and original tags.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "candidate-review.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-candidate', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-candidate',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            tag_observations=[SourceTagRecord(format_name='flac', tag_name='TITLE', value='Old title')],
            candidates=[
                CandidateRecord(
                    candidate_key='release-id',
                    evidence='{"artist":"Artist","release":"Album","score":0.8,"tags":'
                    '{"ALBUM":"Album","ARTIST":"Artist","TITLE":"New title",'
                    '"TRACKNUMBER":"2","TRACKTOTAL":"10","DATE":"2020"}}',
                )
            ],
        )
        session.add_all((record, source))
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    # When: the reviewer confirms the selected candidate.
    response = client.post(
        '/api/library/records/record-candidate/sources/source-candidate/candidates/select',
        json={'candidate_key': 'release-id'},
    )

    # Then: the selected provider tags become Final and a publication job is queued.
    assert response.status_code == 200
    with Session(engine) as session:
        persisted = session.get(LibraryRecord, 'record-candidate')
        assert persisted is not None
        assert persisted.match_state == 'matched'
        assert persisted.metadata_revisions[-1].layer == 'final'
        assert 'New title' in persisted.metadata_revisions[-1].tags_json
        assert session.query(JobRecord).filter_by(source_id='source-candidate', kind='final_publish').count() == 1


def test_library_api_confirms_acoustid_candidate_and_queues_musicbrainz_analysis(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "acoustid-candidate-review.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-acoustid', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-acoustid',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            candidates=[
                CandidateRecord(
                    candidate_key='recording-id',
                    evidence='{"provider":"acoustid","recording_mbid":"recording-id","score":0.99,"tags":{}}',
                )
            ],
        )
        session.add_all((record, source))
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))
    response = client.post(
        '/api/library/records/record-acoustid/sources/source-acoustid/candidates/select',
        json={'candidate_key': 'recording-id', 'provider': 'acoustid'},
    )

    assert response.status_code == 200
    with Session(engine) as session:
        persisted = session.get(LibraryRecord, 'record-acoustid')
        assert persisted is not None
        assert persisted.musicbrainz_recording_id == 'recording-id'
        assert persisted.musicbrainz_release_id is None
        assert session.query(JobRecord).filter_by(source_id='source-acoustid', kind='musicbrainz_analysis').count() == 1


def test_library_api_decodes_acoustid_candidate_with_musicbrainz(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "acoustid-decode.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-decode', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-decode',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            tag_observations=[SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Fixture Album')],
            candidates=[
                CandidateRecord(
                    candidate_key='recording-id',
                    evidence='{"provider":"acoustid","recording_mbid":"recording-id","score":0.99,"tags":{}}',
                )
            ],
        )
        cached_response = b'{"outcome":"no_match"}'
        session.add_all(
            (
                record,
                source,
                ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=timestamp),
                ProviderSnapshotRecord(
                    provider_name='musicbrainz',
                    request_hash=sha256(b'recording:recording-id').hexdigest(),
                    request_descriptor='musicbrainz v2 lookup',
                    response_sha256=sha256(cached_response).hexdigest(),
                    response_body=cached_response,
                    captured_at=datetime(2026, 8, 8, tzinfo=UTC),
                    outcome='no_match',
                    state='fresh',
                    http_status=200,
                ),
            )
        )
        session.commit()

    provider = MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz')
    client = TestClient(create_app(lambda: Session(engine), musicbrainz_provider=provider))
    response = client.get(
        '/api/library/records/record-decode/sources/source-decode/candidates/recording-id/musicbrainz'
    )

    assert response.status_code == 200, response.text
    assert response.json()['artist'] == 'Fixture Artist'
    assert response.json()['album'] == 'Fixture Release'


def test_library_api_hides_legacy_musicbrainz_recording_candidates() -> None:
    legacy = CandidateEvidencePayload(provider='musicbrainz', score=0.99)
    release = CandidateEvidencePayload(
        provider='musicbrainz',
        artist='Fixture Artist',
        release='Fixture Release',
        tags={'MUSICBRAINZ_ALBUMID': 'release-id'},
    )

    assert not _candidate_is_displayable(legacy)
    assert _candidate_is_displayable(release)


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


def test_reconciliation_keeps_current_publication_until_replacement_publishes(tmp_path: Path) -> None:
    # Given: a stable record with a current publication and a changed incoming file.
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    source_path = incoming / 'song.flac'
    source_path.write_bytes(b'new-source')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "replacement-publication.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-replacement-publication', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
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
        publication = LibraryPublicationRecord(
            id='publication-old',
            library_record=record,
            source=source,
            path=str(tmp_path / 'media' / 'song.flac'),
            format_name='flac',
            content_sha256='b' * 64,
            state='current',
            created_at=timestamp,
        )
        session.add_all((record, source, publication))
        session.commit()

        # When: reconciliation discovers the replacement source generation.
        _ = reconcile_incoming(session, incoming)
        session.commit()

        # Then: the old publication remains current until the new audio is published.
        persisted = session.get(LibraryRecord, record.id)
        assert persisted is not None
        assert next(item for item in persisted.publications if item.id == 'publication-old').state == 'current'
