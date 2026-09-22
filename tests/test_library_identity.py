from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from typing import get_args

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError
from sqlalchemy import create_engine, insert
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.api.candidate_views import (
    _candidate_is_displayable,
    _merge_candidate_evidence,
)
from music_ingest.contracts import CandidateEvidencePayload, LibraryTrackResponse, LyricsStatus
from music_ingest.contracts.api import CandidateScoreComponents
from music_ingest.models import (
    Base,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
    ProviderAttemptRecord,
    ProviderScheduleRecord,
    ProviderSnapshotRecord,
    PublicationAttemptRecord,
    SourceRecord,
    SourceRecordingAssignmentRecord,
    SourceRootRecord,
    SourceTagRecord,
)
from music_ingest.services.library.service import append_metadata_revision, attach_source
from music_ingest.services.reconciliation import (
    apply_reconciliation_plan,
    load_reconciliation_snapshot,
    plan_reconciliation,
)
from tests.support.providers import MusicBrainzFixtureProvider


def test_candidate_evidence_merges_provider_scores_for_one_recording_mbid() -> None:
    acoustid = CandidateEvidencePayload(
        provider='acoustid',
        entity='recording',
        recording_mbid='recording-id',
        score=0.92,
        tags={'MUSICBRAINZ_RECORDINGID': 'recording-id'},
    )
    musicbrainz = CandidateEvidencePayload(
        provider='musicbrainz',
        entity='recording',
        recording_mbid='recording-id',
        artist='Fixture Artist',
        title='Fixture Track',
        release='Fixture Album',
        release_mbid='release-id',
        score=0.61,
        score_components=CandidateScoreComponents(artist=0.4, release=0.2, duration=0.01),
        tags={'TITLE': 'Fixture Track'},
    )

    merged = _merge_candidate_evidence(acoustid, musicbrainz)

    assert merged.provider == 'musicbrainz'
    assert merged.score == 0.61
    assert merged.acoustid_score == 0.92
    assert merged.musicbrainz_score is None
    assert merged.title == 'Fixture Track'
    assert merged.release_mbid == 'release-id'


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


def test_library_record_lyric_state_defaults_and_round_trips_with_publication_association(tmp_path: Path) -> None:
    # Given: a library record with an intake source and a current managed publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-state.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        record = LibraryRecord(id='record-lyrics', created_at=observed_at, updated_at=observed_at)
        source = SourceRecord(
            id='source-lyrics',
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
        publication = LibraryPublicationRecord(
            id='publication-lyrics',
            library_record=record,
            source=source,
            path='Artist/Album/song.flac',
            format_name='flac',
            content_sha256='b' * 64,
            state='current',
            created_at=observed_at,
        )
        session.add_all((record, source, publication))
        session.commit()

        # Then: a record without lyric work owns the neutral lyric state and no lyric payload.
        assert record.lyrics_status == 'none'
        assert (record.lyrics_path, record.lyrics_publication_id, record.lyrics_sha256, record.lyrics_updated_at) == (
            None,
            None,
            None,
            None,
        )

        # When: synced lyrics are materialized for that record.
        record.lyrics_status = 'synced'
        record.lyrics_path = 'Artist/Album/song.lrc'
        record.lyrics_publication_id = publication.id
        record.lyrics_sha256 = 'c' * 64
        record.lyrics_updated_at = observed_at
        session.commit()
        session.expire_all()
        persisted = session.get(LibraryRecord, 'record-lyrics')

    # Then: the current lyric state is read back from the record itself, without affecting publications.
    assert persisted is not None
    assert persisted.lyrics_status == 'synced'
    assert persisted.lyrics_path == 'Artist/Album/song.lrc'
    assert persisted.lyrics_publication_id == 'publication-lyrics'
    assert persisted.lyrics_sha256 == 'c' * 64
    assert persisted.lyrics_updated_at is not None
    assert persisted.lyrics_updated_at.replace(tzinfo=UTC) == observed_at
    assert [publication.id for publication in persisted.publications] == ['publication-lyrics']


def test_library_record_lyric_state_rejects_undeclared_status(tmp_path: Path) -> None:
    # Given: a persisted library record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-state-check.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        session.add(LibraryRecord(id='record-invalid-lyrics', created_at=observed_at, updated_at=observed_at))
        session.commit()
        record = session.get(LibraryRecord, 'record-invalid-lyrics')
        assert record is not None

        # When: an undeclared lyric state is persisted.
        record.lyrics_status = 'downloaded'

        # Then: the persisted lyric state stays constrained to the declared vocabulary.
        with pytest.raises(IntegrityError):
            session.commit()


def test_append_metadata_revision_when_relationship_is_stale_uses_durable_revision(tmp_path: Path) -> None:
    # Given: a loaded metadata relationship while another write adds the next final revision.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "metadata-revisions.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-revisions', created_at=observed_at, updated_at=observed_at)
        source = SourceRecord(
            id='source-revisions',
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
        session.add_all((record, source))
        _ = append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'First'}, 'worker', observed_at)
        session.commit()
        _ = record.metadata_revisions
        _ = session.execute(
            insert(LibraryMetadataRevisionRecord).values(
                library_record_id=record.id,
                source_id=source.id,
                layer='final',
                revision=2,
                tags_json='{"TITLE":"Second"}',
                actor='provider',
                created_at=observed_at,
            )
        )

        # When: a provider appends final metadata through the stale relationship.
        appended = append_metadata_revision(
            session,
            record.id,
            source.id,
            'final',
            {'TITLE': 'Third'},
            'provider',
            observed_at,
        )

        # Then: its revision follows the durable maximum rather than colliding.
        assert appended.revision == 3


def test_attach_source_when_moving_a_source_keeps_existing_publication_history(tmp_path: Path) -> None:
    # Given: a source and its immutable publication history on one library record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "attachment-history.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        original = LibraryRecord(id='record-original', created_at=observed_at, updated_at=observed_at)
        destination = LibraryRecord(id='record-destination', created_at=observed_at, updated_at=observed_at)
        source = SourceRecord(
            id='source-history',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=original,
        )
        publication = LibraryPublicationRecord(
            id='publication-history',
            library_record=original,
            source=source,
            path='Artist/Album/song.flac',
            format_name='flac',
            content_sha256='b' * 64,
            state='current',
            created_at=observed_at,
        )
        session.add_all((original, destination, source, publication))
        session.commit()

        # When: existing attachment mechanics move the source to another record.
        _ = attach_source(session, source.id, destination.id, now=observed_at)
        session.commit()

        # Then: source association changes without deleting immutable output history.
        persisted_source = session.get(SourceRecord, source.id)
        assert persisted_source is not None
        assert persisted_source.library_record_id == destination.id
        assert session.get(LibraryPublicationRecord, publication.id) is not None


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


def test_library_api_filters_catalog_in_sql_by_release_and_name(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "library-filter.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        matched_release = LibraryRecord(
            id='record-release',
            musicbrainz_release_id='release-shared',
            processing_state='analyzing',
            match_state='unmatched',
            publication_state='current',
            created_at=timestamp,
            updated_at=timestamp,
        )
        matched_name_only = LibraryRecord(
            id='record-name-only',
            processing_state='needs_review',
            match_state='needs_review',
            created_at=timestamp,
            updated_at=timestamp,
        )
        other_album = LibraryRecord(id='record-other', created_at=timestamp, updated_at=timestamp)
        session.add_all(
            (
                matched_release,
                matched_name_only,
                other_album,
                SourceRecord(
                    id='source-release',
                    source_path='/release.flac',
                    device=1,
                    inode=1,
                    size_bytes=1,
                    sha256='a' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    library_record=matched_release,
                    tag_observations=[
                        SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Artist/Side'),
                        SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Shared'),
                    ],
                ),
                SourceRecord(
                    id='source-name-only',
                    source_path='/name-only.flac',
                    device=1,
                    inode=2,
                    size_bytes=1,
                    sha256='b' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='disappeared',
                    library_record=matched_name_only,
                    tag_observations=[
                        SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Artist/Side'),
                        SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Shared'),
                    ],
                ),
                SourceRecord(
                    id='source-other',
                    source_path='/other.flac',
                    device=1,
                    inode=3,
                    size_bytes=1,
                    sha256='c' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    library_record=other_album,
                    tag_observations=[
                        SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Other Artist'),
                        SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Shared'),
                    ],
                ),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    artists = client.get('/api/library/artists')
    albums = client.get('/api/library/albums?artist=Artist%2FSide')
    release = client.get('/api/library/tracks?artist=Artist%2FSide&album_id=release-shared')
    name_only = client.get('/api/library/tracks?artist=Artist%2FSide&album_name=Shared')

    assert artists.json()['items'] == [
        {'name': 'Artist/Side', 'track_count': 2},
        {'name': 'Other Artist', 'track_count': 1},
    ]
    assert albums.json()['items'] == [
        {'album_id': None, 'album_name': 'Shared', 'track_count': 1, 'artwork_url': None},
        {'album_id': 'release-shared', 'album_name': 'Shared', 'track_count': 1, 'artwork_url': None},
    ]
    assert [item['record_id'] for item in release.json()['items']] == ['record-release']
    assert [item['record_id'] for item in name_only.json()['items']] == ['record-name-only']
    assert release.json()['items'][0] == {
        'record_id': 'record-release',
        'source_id': 'source-release',
        'source_path': '/release.flac',
        'artist_name': 'Artist/Side',
        'album_name': 'Shared',
        'album_id': 'release-shared',
        'title': '',
        'track_number': None,
        'source_state': 'present',
        'processing_state': 'analyzing',
        'match_state': 'unmatched',
        'publication_state': 'current',
        'lyrics_status': 'none',
        'lyrics_synced': False,
    }
    assert name_only.json()['items'][0]['source_state'] == 'disappeared'
    assert name_only.json()['items'][0]['processing_state'] == 'needs_review'
    assert name_only.json()['items'][0]['match_state'] == 'needs_review'

    invalid_tracks = client.get('/api/library/tracks?artist=Artist%2FSide')
    assert invalid_tracks.status_code == 422
    assert (
        'exactly one of album_id, album_name, or album_missing=true is required'
        in invalid_tracks.json()['detail'][0]['msg']
    )

    openapi = client.get('/openapi.json').json()
    assert openapi['paths']['/api/library/albums']['get']['responses']['200']['content']['application/json']


def test_library_api_album_tracks_uses_effective_source_once_per_record(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "effective-source-catalog.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-effective-source', created_at=timestamp, updated_at=timestamp)
        mp3 = SourceRecord(
            id='source-effective-mp3',
            source_path='/incoming/song.mp3',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            tag_observations=[
                SourceTagRecord(format_name='mp3', tag_name='ALBUMARTIST', value='Fixture Artist'),
                SourceTagRecord(format_name='mp3', tag_name='ALBUM', value='Fixture Album'),
                SourceTagRecord(format_name='mp3', tag_name='TITLE', value='MP3 title'),
            ],
        )
        flac = SourceRecord(
            id='source-effective-flac',
            source_path='/incoming/song.flac',
            device=1,
            inode=2,
            size_bytes=1,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            tag_observations=[
                SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Fixture Artist'),
                SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Fixture Album'),
                SourceTagRecord(format_name='flac', tag_name='TITLE', value='FLAC title'),
            ],
        )
        decision = EffectiveSourceDecisionRecord(
            library_record=record,
            source_id=flac.id,
            baseline_source_id=mp3.id,
            policy_version='v1',
            quality_tuple_json='[]',
            reason='fixture',
            updated_at=timestamp,
        )
        session.add_all((record, mp3, flac, decision))
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).get(
        '/api/library/tracks?artist=Fixture%20Artist&album_name=Fixture%20Album'
    )

    assert response.status_code == 200
    assert [item['source_id'] for item in response.json()['items']] == ['source-effective-flac']
    assert response.json()['items'][0]['source_path'] == '/incoming/song.flac'
    assert response.json()['items'][0]['album_name'] == 'Fixture Album'
    assert response.json()['items'][0]['title'] == 'FLAC title'


def test_library_api_confirms_provider_candidate_into_final_publication_job(tmp_path: Path) -> None:
    # Given: a source with a stored MusicBrainz candidate and original tags.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "candidate-review.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-candidate', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-candidate',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            tag_observations=[SourceTagRecord(format_name='flac', tag_name='TITLE', value='Old title')],
            candidates=[
                CandidateRecord(
                    candidate_key='release-id:recording-id',
                    evidence='{"provider":"musicbrainz","entity":"recording_release","artist":"Artist",'
                    '"release":"Album","score":0.8,"release_mbid":"release-id",'
                    '"recording_mbid":"recording-id","tags":{"ALBUM":"Album","ARTIST":"Artist",'
                    '"TITLE":"New title","TRACKNUMBER":"2","TRACKTOTAL":"10","DATE":"2020",'
                    '"MUSICBRAINZ_ALBUMID":"release-id","MUSICBRAINZ_RECORDINGID":"recording-id"}}',
                )
            ],
        )
        session.add_all((root, record, source))
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    # When: the reviewer confirms the selected candidate.
    response = client.post(
        '/api/library/records/record-candidate/sources/source-candidate/candidates/select',
        json={'candidate_key': 'release-id:recording-id', 'entity': 'recording_release'},
    )

    # Then: the selected provider tags become Final and a publication job is queued.
    assert response.status_code == 200
    with Session(engine) as session:
        persisted = session.get(LibraryRecord, 'record-candidate')
        assert persisted is not None
        assert persisted.match_state == 'matched'
        assert persisted.metadata_revisions[-1].layer == 'final'
        assert 'New title' in persisted.metadata_revisions[-1].tags_json
        refreshes = session.query(JobRecord).filter_by(library_record_id='record-candidate', kind='selection_refresh')
        assert refreshes.count() == 1


def test_library_api_confirms_acoustid_candidate_and_queues_musicbrainz_analysis(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "acoustid-candidate-review.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-acoustid', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-acoustid',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            candidates=[
                CandidateRecord(
                    candidate_key='recording-id',
                    evidence='{"provider":"acoustid","recording_mbid":"recording-id","score":0.99,"tags":{}}',
                )
            ],
        )
        session.add_all((root, record, source))
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


def test_library_api_confirms_acoustid_candidate_without_erasing_another_release_identity(tmp_path: Path) -> None:
    # Given: a historical alias that retains the same recording identity without a release identity.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "acoustid-alias.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-canonical', created_at=timestamp, updated_at=timestamp)
        alias = LibraryRecord(
            id='record-alias',
            musicbrainz_recording_id='recording-id',
            created_at=timestamp,
            updated_at=timestamp,
        )
        source = SourceRecord(
            id='source-acoustid-alias',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            candidates=[
                CandidateRecord(
                    candidate_key='recording-id',
                    evidence='{"provider":"acoustid","recording_mbid":"recording-id","score":0.99,"tags":{}}',
                )
            ],
        )
        session.add_all(
            (
                root,
                record,
                alias,
                source,
                LibraryRecordConsolidationRecord(
                    retired_library_record_id=alias.id,
                    canonical_library_record_id=record.id,
                    sha256='a' * 64,
                    created_at=timestamp,
                ),
            )
        )
        session.commit()

    # When: the reviewer confirms that same AcousticID recording on the canonical aggregate.
    response = TestClient(create_app(lambda: Session(engine))).post(
        '/api/library/records/record-canonical/sources/source-acoustid-alias/candidates/select',
        json={'candidate_key': 'recording-id', 'provider': 'acoustid'},
    )

    # Then: the canonical record takes the current identity without erasing the other exact identity.
    assert response.status_code == 200
    with Session(engine) as session:
        canonical = session.get(LibraryRecord, 'record-canonical')
        retained_alias = session.get(LibraryRecord, 'record-alias')
        assert canonical is not None
        assert retained_alias is not None
        assert canonical.musicbrainz_recording_id == 'recording-id'
        assert retained_alias.musicbrainz_recording_id == 'recording-id'


@pytest.mark.parametrize('retained', ['none', 'current', 'prepared', 'exposed', 'failed'])
def test_library_api_recording_override_moves_only_selected_source_and_preserves_evidence(
    tmp_path: Path, retained: str
) -> None:
    # Given: two sources on separate records and provider evidence for the source to move.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "recording-override.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(
            id='record-recording-override',
            musicbrainz_recording_id='11111111-1111-4111-8111-111111111111',
            created_at=timestamp,
            updated_at=timestamp,
        )
        target_record = LibraryRecord(
            id='record-recording-target',
            musicbrainz_recording_id='f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
            created_at=timestamp,
            updated_at=timestamp,
        )
        source = SourceRecord(
            id='source-recording-override',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='acoustid', outcome='success', snapshot_sha256='b' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(candidate_key='provider-evidence', evidence='{"provider":"acoustid","score":0.99}')
            ],
        )
        sibling = SourceRecord(
            id='source-recording-target',
            source_path=str(incoming / 'target.flac'),
            device=3,
            inode=4,
            size_bytes=5,
            sha256='c' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=target_record,
        )
        session.add_all((root, record, target_record, source, sibling))
        if retained == 'current':
            session.add(
                LibraryPublicationRecord(
                    id='retained',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(tmp_path / 'managed.mka'),
                    format_name='mka',
                    content_sha256='e' * 64,
                    state='current',
                    created_at=timestamp,
                )
            )
        elif retained != 'none':
            session.add(
                PublicationAttemptRecord(
                    id='retained',
                    library_record_id=record.id,
                    source_id=source.id,
                    state=retained,
                    target_directory=str(tmp_path),
                    target_audio_name='managed.mka',
                    staging_directory=str(tmp_path / 'staged'),
                    backup_directory=str(tmp_path / 'backup'),
                    created_at=timestamp,
                )
            )
        session.commit()

    client = TestClient(
        create_app(
            lambda: Session(engine),
            musicbrainz_provider=MusicBrainzFixtureProvider(Path('tests/fixtures/musicbrainz')),
        )
    )

    # When: a reviewer corrects only the selected source recording.
    response = client.post(
        '/api/library/records/record-recording-override/sources/source-recording-override/musicbrainz/override',
        json={
            'recording_mbid': 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
        },
    )

    # Then: only that source moves and the before/after audit, evidence, and refreshes survive.
    assert response.status_code == 200
    assert response.json() == {
        'recording_mbid': 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
        'record_id': 'record-recording-target',
    }
    with Session(engine) as session:
        moved = session.get(SourceRecord, 'source-recording-override')
        unchanged = session.get(SourceRecord, 'source-recording-target')
        assert moved is not None
        assert unchanged is not None
        assert moved.library_record_id == 'record-recording-target'
        assert unchanged.library_record_id == 'record-recording-target'
        assert len(moved.provider_attempts) == 1
        assert len(moved.candidates) == 1
        assignments = session.query(SourceRecordingAssignmentRecord).filter_by(source_id=moved.id).all()
        assert len(assignments) == 1
        assert 'record-recording-override' in assignments[0].evidence_json
        assert 'record-recording-target' in assignments[0].evidence_json
        original_record = session.get(LibraryRecord, 'record-recording-override')
        persisted_target_record = session.get(LibraryRecord, 'record-recording-target')
        assert original_record is not None
        assert persisted_target_record is not None
        assert {event.kind for event in original_record.events} >= {'source_recording_reassigned'}
        assert {event.kind for event in persisted_target_record.events} >= {'source_recording_reassigned'}
        assert {
            job.library_record_id for job in session.query(JobRecord).filter_by(kind='selection_refresh').all()
        } == (
            {'record-recording-target', 'record-recording-override'}
            if retained in {'current', 'prepared', 'exposed'}
            else {'record-recording-target'}
        )


def test_library_api_recording_override_when_provider_is_unavailable_keeps_source_in_place(tmp_path: Path) -> None:
    # Given: a source whose requested recording cannot be verified by MusicBrainz.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "recording-override-unavailable.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-unavailable', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-unavailable',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
        )
        session.add_all((root, record, source))
        session.commit()

    client = TestClient(
        create_app(
            lambda: Session(engine),
            musicbrainz_provider=MusicBrainzFixtureProvider(Path('tests/fixtures/musicbrainz_unavailable')),
        )
    )

    # When: MusicBrainz returns unavailable for a typed override request.
    invalid_response = client.post(
        '/api/library/records/record-unavailable/sources/source-unavailable/musicbrainz/override',
        json={'recording_mbid': 'not-a-uuid'},
    )
    response = client.post(
        '/api/library/records/record-unavailable/sources/source-unavailable/musicbrainz/override',
        json={
            'recording_mbid': '3c32b3e7-f21d-4935-bcac-d9c0df46db68',
        },
    )

    # Then: the API reports unavailable validation and leaves the association unchanged.
    assert invalid_response.status_code == 422
    assert response.status_code == 503
    with Session(engine) as session:
        source = session.get(SourceRecord, 'source-unavailable')
        assert source is not None
        assert source.library_record_id == 'record-unavailable'


def test_library_api_recording_override_when_evidence_conflicts_persists_review_before_409(tmp_path: Path) -> None:
    # Given: a source with retained AcoustID evidence for a different recording.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "recording-override-conflict.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    song_path = incoming / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-conflict-api', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-conflict-api',
            source_path=str(song_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            candidates=[
                CandidateRecord(
                    candidate_key='other-recording',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.99,"tags":'
                        '{"MUSICBRAINZ_TRACKID":"11111111-1111-4111-8111-111111111111"}}'
                    ),
                )
            ],
        )
        session.add_all((root, record, source))
        session.commit()

    client = TestClient(
        create_app(
            lambda: Session(engine), musicbrainz_provider=MusicBrainzFixtureProvider(Path('tests/fixtures/musicbrainz'))
        )
    )

    # When: the API receives a verified MBID absent from the source's candidates.
    response = client.post(
        '/api/library/records/record-conflict-api/sources/source-conflict-api/musicbrainz/override',
        json={
            'recording_mbid': 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
        },
    )

    # Then: the selected source is moved to the requested recording aggregate.
    assert response.status_code == 200
    with Session(engine) as session:
        source = session.get(SourceRecord, 'source-conflict-api')
        record = session.get(LibraryRecord, 'record-conflict-api')
        assert source is not None
        assert record is not None
        assert source.library_record_id != record.id
        target = session.get(LibraryRecord, source.library_record_id)
        assert target is not None
        assert target.musicbrainz_recording_id == 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'


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
    assert not _candidate_is_displayable(release)


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


def test_library_catalog_keeps_empty_record_after_source_reassignment(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "empty-record.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(LibraryRecord(id='empty-record', created_at=datetime.now(UTC), updated_at=datetime.now(UTC)))
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).get('/api/library/records')

    assert response.status_code == 200
    assert response.json()['items'][0]['record_id'] == 'empty-record'
    assert response.json()['items'][0]['sources'] == []


def test_library_catalog_counts_only_present_sources(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "catalog-source-count.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        source_record = LibraryRecord(id='record-with-source', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-present',
            source_path='/incoming/noize.flac',
            device=1,
            inode=1,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=source_record,
        )
        source.tag_observations = [
            SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Noize MC'),
            SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Present Album'),
        ]
        orphan_record = LibraryRecord(id='record-without-source', created_at=timestamp, updated_at=timestamp)
        orphan_revision = LibraryMetadataRevisionRecord(
            library_record=orphan_record,
            source_id='source-missing',
            layer='original',
            revision=1,
            tags_json='{"ALBUMARTIST":"Noize MC","ALBUM":"Orphan Album"}',
            actor='test',
            created_at=timestamp,
        )
        session.add_all((source_record, source, orphan_record, orphan_revision))
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).get('/api/library/artists')

    assert response.status_code == 200
    assert response.json()['items'] == [{'name': 'Noize MC', 'track_count': 1}]


def test_library_artists_groups_records_without_artist_tags_as_unknown(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "unknown-artist.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-unknown', created_at=timestamp, updated_at=timestamp)
        source = SourceRecord(
            id='source-unknown',
            source_path='/incoming/unknown.flac',
            device=1,
            inode=1,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).get('/api/library/artists')

    assert response.status_code == 200
    assert response.json() == {
        'items': [{'name': None, 'track_count': 1}],
        'total_track_count': 1,
    }

    albums_response = TestClient(create_app(lambda: Session(engine))).get('/api/library/albums?artist_missing=true')

    assert albums_response.status_code == 200
    assert albums_response.json()['items'] == [
        {'album_id': None, 'album_name': None, 'track_count': 1, 'artwork_url': None}
    ]

    tracks_response = TestClient(create_app(lambda: Session(engine))).get(
        '/api/library/tracks?artist_missing=true&album_missing=true'
    )

    assert tracks_response.status_code == 200
    assert tracks_response.json()['items'][0] == {
        'record_id': 'record-unknown',
        'source_id': 'source-unknown',
        'source_path': '/incoming/unknown.flac',
        'artist_name': None,
        'album_name': None,
        'album_id': None,
        'title': '',
        'track_number': None,
        'source_state': 'present',
        'processing_state': 'queued',
        'match_state': 'unmatched',
        'publication_state': 'absent',
        'lyrics_status': 'none',
        'lyrics_synced': False,
    }


def test_manual_actions_api_filters_records_and_returns_category_counts(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "manual-actions-api.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(tmp_path),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        analysis_error = LibraryRecord(
            id='record-analysis-error',
            processing_state='blocked_infrastructure',
            created_at=timestamp,
            updated_at=timestamp,
        )
        needs_review = LibraryRecord(
            id='record-needs-review',
            processing_state='needs_review',
            created_at=timestamp,
            updated_at=timestamp,
        )
        complete = LibraryRecord(
            id='record-complete', processing_state='complete', created_at=timestamp, updated_at=timestamp
        )
        session.add_all(
            (
                root,
                analysis_error,
                needs_review,
                complete,
                SourceRecord(
                    id='source-analysis-error',
                    source_path='/incoming/error.flac',
                    device=1,
                    inode=1,
                    size_bytes=1,
                    sha256='a' * 64,
                    duration_seconds=1,
                    origin='manual',
                    intake_state='present',
                    source_root=root,
                    library_record=analysis_error,
                ),
                SourceRecord(
                    id='source-needs-review',
                    source_path='/incoming/review.flac',
                    device=1,
                    inode=2,
                    size_bytes=1,
                    sha256='b' * 64,
                    duration_seconds=1,
                    origin='manual',
                    intake_state='present',
                    source_root=root,
                    library_record=needs_review,
                ),
                SourceRecord(
                    id='source-complete',
                    source_path='/incoming/complete.flac',
                    device=1,
                    inode=3,
                    size_bytes=1,
                    sha256='c' * 64,
                    duration_seconds=1,
                    origin='manual',
                    intake_state='present',
                    source_root=root,
                    library_record=complete,
                ),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))
    analysis_response = client.get('/api/library/manual-actions?action=analysis-error')
    review_response = client.get('/api/library/manual-actions?action=needs-review')

    assert analysis_response.status_code == 200
    assert [item['record_id'] for item in analysis_response.json()['items']] == ['record-analysis-error']
    assert analysis_response.json()['counts'] == {'analysis_error': 1, 'needs_review': 1}
    assert review_response.status_code == 200
    assert [item['record_id'] for item in review_response.json()['items']] == ['record-needs-review']


def test_analysis_retry_api_requeues_failed_and_missing_provider_work_without_duplicate_jobs(tmp_path: Path) -> None:
    # Given: one failed source, one source never sent to a provider, and one successful source.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider-retry.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    source_root = tmp_path / 'incoming'
    source_root.mkdir()
    failed_path = source_root / 'failed.flac'
    never_sent_path = source_root / 'never.flac'
    successful_path = source_root / 'success.flac'
    replaced_path = source_root / 'replaced.flac'
    for source_path in (failed_path, never_sent_path, successful_path, replaced_path):
        _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(source_root),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        record = LibraryRecord(id='record-retry', created_at=timestamp, updated_at=timestamp)
        failed = SourceRecord(
            id='source-failed',
            source_path=str(failed_path),
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
        )
        never_sent = SourceRecord(
            id='source-never',
            source_path=str(never_sent_path),
            device=1,
            inode=3,
            size_bytes=4,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
        )
        successful = SourceRecord(
            id='source-success',
            source_path=str(successful_path),
            device=1,
            inode=4,
            size_bytes=5,
            sha256='c' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
        )
        replaced = SourceRecord(
            id='source-replaced',
            source_path=str(replaced_path),
            device=1,
            inode=5,
            size_bytes=6,
            sha256='f' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='replaced',
            source_root=root,
            library_record=record,
        )
        session.add_all(
            (
                root,
                record,
                failed,
                never_sent,
                successful,
                replaced,
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
                JobRecord(
                    id='job-replaced',
                    source_id='source-replaced',
                    kind='filesystem_scan',
                    state='completed',
                    created_at=timestamp,
                ),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator retries one failed source, a replaced source, and then requests the bulk provider retry.
    single = client.post('/api/library/records/record-retry/sources/source-failed/provider-retry')
    replaced_retry = client.post('/api/library/records/record-retry/sources/source-replaced/provider-retry')
    bulk = client.post('/api/library/providers/retry')

    # Then: the replaced source remains terminal, while the active sources retain existing retry behavior.
    assert single.status_code == 200
    assert single.json() == {'source_id': 'source-failed', 'queued': True}
    assert replaced_retry.status_code == 200
    assert replaced_retry.json() == {'source_id': 'source-replaced', 'queued': False}
    assert bulk.status_code == 200
    assert bulk.json() == {'queued': 1}
    with Session(engine) as session:
        jobs = {job.source_id: job for job in session.query(JobRecord).all()}
        assert jobs['source-failed'].state == 'queued'
        assert jobs['source-failed'].kind == 'acoustid_analysis'
        assert jobs['source-never'].state == 'queued'
        assert jobs['source-success'].state == 'completed'
        assert jobs['source-replaced'].state == 'completed'
        assert len(jobs) == 4

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
        root = SourceRootRecord(
            id='root-incoming',
            display_name='Incoming',
            canonical_path=str(incoming.resolve()),
            enabled=True,
            scan_state='never_scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
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
                source_root=root,
                library_record=record,
            )
        )
        session.commit()

        observed_at = datetime.now(UTC)
        result = apply_reconciliation_plan(
            session, plan_reconciliation(load_reconciliation_snapshot(session, observed_at)), observed_at
        )
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
        root = SourceRootRecord(
            id='root-incoming',
            display_name='Incoming',
            canonical_path=str(incoming.resolve()),
            enabled=True,
            scan_state='never_scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
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
            source_root=root,
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
        observed_at = datetime.now(UTC)
        _ = apply_reconciliation_plan(
            session, plan_reconciliation(load_reconciliation_snapshot(session, observed_at)), observed_at
        )
        session.commit()

        # Then: the old publication remains current until the new audio is published.
        persisted = session.get(LibraryRecord, record.id)
        assert persisted is not None
        assert next(item for item in persisted.publications if item.id == 'publication-old').state == 'current'


def _lyrics_album_record(
    record_id: str,
    source_id: str,
    *,
    lyrics_status: str,
    title: str,
    timestamp: datetime,
) -> tuple[LibraryRecord, SourceRecord]:
    record = LibraryRecord(
        id=record_id,
        processing_state='ready',
        publication_state='current',
        lyrics_status=lyrics_status,
        created_at=timestamp,
        updated_at=timestamp,
    )
    source = SourceRecord(
        id=source_id,
        source_path=f'/incoming/{source_id}.flac',
        device=1,
        inode=1,
        size_bytes=3,
        sha256='a' * 64,
        duration_seconds=180,
        origin='manual',
        intake_state='present',
        library_record=record,
        tag_observations=[
            SourceTagRecord(format_name='flac', tag_name='ALBUMARTIST', value='Noize MC'),
            SourceTagRecord(format_name='flac', tag_name='ALBUM', value='Lyrics Album'),
            SourceTagRecord(format_name='flac', tag_name='TITLE', value=title),
        ],
    )
    return record, source


def test_library_tracks_expose_materialized_lyrics_status(tmp_path: Path) -> None:
    # Given: two tracks whose materialized lyric state differs and a stale lyric history row.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "track-lyrics-status.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        synced, synced_source = _lyrics_album_record(
            'record-synced', 'source-synced', lyrics_status='synced', title='Synced Song', timestamp=timestamp
        )
        missing, missing_source = _lyrics_album_record(
            'record-missing',
            'source-missing',
            lyrics_status='no_candidate',
            title='Plain Song',
            timestamp=timestamp,
        )
        stale_history = LibraryEventRecord(
            library_record=missing,
            source_id='source-missing',
            kind='lyrics',
            state='synced',
            reason='stale lyric history',
            details_json='{}',
            created_at=timestamp,
        )
        session.add_all((synced, synced_source, missing, missing_source, stale_history))
        session.commit()

    # When: the album's tracks are read through the catalog API.
    response = TestClient(create_app(lambda: Session(engine))).get(
        '/api/library/tracks?artist=Noize%20MC&album_name=Lyrics%20Album'
    )

    # Then: each track reports its materialized lyric state without leaking history or sidecar payloads.
    assert response.status_code == 200
    items = {item['record_id']: item for item in response.json()['items']}
    assert items['record-synced']['lyrics_status'] == 'synced'
    assert items['record-synced']['lyrics_synced'] is True
    assert items['record-missing']['lyrics_status'] == 'no_candidate'
    assert items['record-missing']['lyrics_synced'] is False
    for item in items.values():
        assert set(item) == {
            'record_id',
            'source_id',
            'source_path',
            'artist_name',
            'album_name',
            'album_id',
            'title',
            'track_number',
            'source_state',
            'processing_state',
            'match_state',
            'publication_state',
            'lyrics_status',
            'lyrics_synced',
        }


def test_library_record_detail_exposes_materialized_lyrics_status(tmp_path: Path) -> None:
    # Given: one record with synced lyrics and one whose lyric validation was rejected.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "detail-lyrics-status.db"}')
    Base.metadata.create_all(engine)
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)
    with Session(engine) as session:
        synced, synced_source = _lyrics_album_record(
            'record-synced', 'source-synced', lyrics_status='synced', title='Synced Song', timestamp=timestamp
        )
        rejected, rejected_source = _lyrics_album_record(
            'record-rejected',
            'source-rejected',
            lyrics_status='validation_rejected',
            title='Rejected Song',
            timestamp=timestamp,
        )
        session.add_all((synced, synced_source, rejected, rejected_source))
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))

    # When: each record is read through the detail API.
    synced_response = client.get('/api/library/records/record-synced')
    rejected_response = client.get('/api/library/records/record-rejected')

    # Then: the detail reflects the materialized lyric state and an explicit synchronized boolean.
    assert synced_response.status_code == 200
    assert synced_response.json()['lyrics_status'] == 'synced'
    assert synced_response.json()['lyrics_synced'] is True
    assert rejected_response.status_code == 200
    assert rejected_response.json()['lyrics_status'] == 'validation_rejected'
    assert rejected_response.json()['lyrics_synced'] is False


def test_track_response_lyrics_status_accepts_only_declared_states() -> None:
    # Given: the declared lyric statuses mirroring the persisted check constraint.
    assert get_args(LyricsStatus) == (
        'none',
        'pending',
        'synced',
        'no_candidate',
        'validation_rejected',
        'error',
    )

    # When/Then: a declared status is accepted and an undeclared one is rejected.
    track = LibraryTrackResponse(
        record_id='record-1',
        source_id='source-1',
        source_path='/incoming/song.flac',
        artist_name=None,
        album_name=None,
        album_id=None,
        title='Song',
        track_number=None,
        source_state='present',
        processing_state='ready',
        match_state='matched',
        publication_state='current',
        lyrics_status='synced',
        lyrics_synced=True,
    )
    assert track.lyrics_status == 'synced'
    assert track.lyrics_synced is True
    with pytest.raises(ValidationError):
        LibraryTrackResponse.model_validate({**track.model_dump(), 'lyrics_status': 'downloaded'})
