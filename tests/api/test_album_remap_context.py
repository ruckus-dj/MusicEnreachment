from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import (
    Base,
    FingerprintRecord,
    JobRecord,
    LibraryMetadataRevisionRecord,
    LibraryRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.intake.service import IntakeRequest, Origin, SourceTagObservation, intake_source
from music_ingest.services.matching.providers import MusicBrainzHttpResponse


def test_album_remap_context_search_and_preview_when_selecting_exact_album(tmp_path: Path) -> None:
    # Given: two active sources with exact album tags and distinct current recording identities.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap.db"}')
    Base.metadata.create_all(engine)
    release_id = '11111111-1111-4111-8111-111111111111'
    first_track_id = '22222222-2222-4222-8222-222222222222'
    second_track_id = '33333333-3333-4333-8333-333333333333'
    first_recording_id = '44444444-4444-4444-8444-444444444444'
    second_recording_id = '55555555-5555-4555-8555-555555555555'
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path=str(tmp_path),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        source_ids: list[str] = []
        for position, title, recording_id in ((1, 'First', first_recording_id), (2, 'Second', second_recording_id)):
            path = tmp_path / f'{position}.flac'
            path.write_bytes(title.encode())
            intake = intake_source(
                session,
                IntakeRequest(
                    source_path=path,
                    source_root_id='root',
                    origin=Origin.MANUAL,
                    duration_seconds=180,
                    tag_observations=(
                        SourceTagObservation(format_name='flac', tag_name='ARTIST', value='Artist'),
                        SourceTagObservation(format_name='flac', tag_name='ALBUM', value='Album'),
                        SourceTagObservation(format_name='flac', tag_name='TITLE', value=title),
                        SourceTagObservation(format_name='flac', tag_name='TRACKNUMBER', value=str(position)),
                    ),
                    artwork_observations=(),
                    provider_attempts=(),
                    candidates=(),
                    review_decisions=(),
                ),
            )
            source = session.get(SourceRecord, intake.source_id)
            assert source is not None and source.library_record_id is not None
            record = session.get(LibraryRecord, source.library_record_id)
            assert record is not None
            record.musicbrainz_recording_id = recording_id
            record.musicbrainz_release_id = release_id
            if position == 1:
                source.duration_seconds = None
                source.fingerprints.append(
                    FingerprintRecord(
                        state='success',
                        fingerprint='fixture-fingerprint',
                        duration_seconds=193.167,
                        output_sha256='f' * 64,
                    )
                )
            source_ids.append(str(intake.source_id))
        session.commit()
    with Session(engine) as session:
        _ = JobRepository(session).enqueue_release_artwork(release_id, now)
        session.commit()

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            assert headers['User-Agent'] == 'music-ingest/0.1.0 (music-ingest@example.com)'
            query = parse_qs(urlsplit(url).query)
            if '/release/' in url and 'query' in query:
                return MusicBrainzHttpResponse(
                    200,
                    json.dumps(
                        {'releases': [{'id': release_id, 'title': 'Album', 'artist-credit': [{'name': 'Artist'}]}]}
                    ).encode(),
                )
            return MusicBrainzHttpResponse(
                200,
                json.dumps(
                    {
                        'id': release_id,
                        'title': 'Album',
                        'artist-credit': [{'name': 'Artist'}],
                        'media': [
                            {
                                'position': 1,
                                'pregap': {
                                    'id': '99999999-9999-4999-8999-999999999999',
                                    'position': 0,
                                    'title': 'Pregap',
                                    'recording': {
                                        'id': 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
                                        'title': 'Pregap',
                                    },
                                },
                                'tracks': [
                                    {
                                        'id': first_track_id,
                                        'position': 1,
                                        'title': 'First',
                                        'length': 180000,
                                        'recording': {'id': first_recording_id, 'title': 'First'},
                                    },
                                    {
                                        'id': second_track_id,
                                        'position': 2,
                                        'title': 'Second',
                                        'length': 180000,
                                        'recording': {'id': second_recording_id, 'title': 'Second'},
                                    },
                                ],
                            },
                            {
                                'position': 2,
                                'data-tracks': [
                                    {
                                        'id': '66666666-6666-4666-8666-666666666666',
                                        'position': 1,
                                        'title': 'Data',
                                        'recording': {'id': '77777777-7777-4777-8777-777777777777', 'title': 'Data'},
                                    }
                                ],
                            },
                        ],
                    }
                ).encode(),
            )

    client = TestClient(create_app(lambda: Session(engine), musicbrainz_transport=FixtureTransport()))
    selector = {
        'release_mbid': release_id,
        'artist_name': None,
        'album_name': None,
        'artist_missing': False,
        'album_missing': False,
    }

    # When: the UI loads context, searches by title, and previews the selected release.
    context = client.post('/api/library/album-remaps/context', json={'selector': selector})
    search = client.post('/api/library/album-remaps/releases/search', json={'query': 'Artist Album'})
    direct_search = client.post('/api/library/album-remaps/releases/search', json={'query': release_id})
    preview = client.post(
        '/api/library/album-remaps/preview',
        json={
            'selector': selector,
            'album_snapshot_token': context.json().get('album_snapshot_token'),
            'release_mbid': release_id,
        },
    )

    # Then: the context is complete, the provider result is typed, and multi-disc track slots retain track MBIDs.
    assert context.status_code == 200
    assert [item['source_id'] for item in context.json()['sources']] == sorted(source_ids)
    source_durations = {item['source_id']: item['duration_seconds'] for item in context.json()['sources']}
    assert source_durations[source_ids[0]] == 193
    assert len(context.json()['album_snapshot_token']) == 64
    assert search.status_code == 200
    assert search.json()['releases'][0]['release_mbid'] == release_id
    assert search.json()['releases'][0]['artist_credit'] == 'Artist'
    assert direct_search.status_code == 200
    assert direct_search.json()['releases'][0]['release_mbid'] == release_id
    assert preview.status_code == 200
    slots = preview.json()['track_slots']
    assert [(slot['medium_position'], slot['track_position'], slot['track_mbid']) for slot in slots] == [
        (1, 0, '99999999-9999-4999-8999-999999999999'),
        (1, 1, first_track_id),
        (1, 2, second_track_id),
        (2, 1, '66666666-6666-4666-8666-666666666666'),
    ]
    assert slots[0]['assignable'] is True
    assert slots[1]['suggested_source_id'] == source_ids[0]
    apply = client.post(
        '/api/library/album-remaps/apply',
        json={
            'selector': selector,
            'album_snapshot_token': context.json()['album_snapshot_token'],
            'release_mbid': release_id,
            'release_snapshot_token': preview.json()['release_snapshot_token'],
            'assignments': [
                {'source_id': source_ids[0], 'track_mbid': first_track_id},
                {'source_id': source_ids[1], 'track_mbid': second_track_id},
            ],
            'unmatched_source_ids': [],
        },
    )

    assert apply.status_code == 200
    assert apply.json()['assigned_source_ids'] == sorted(source_ids)
    assert apply.json()['publication_refresh_queued'] is True
    assert apply.json()['queued_release_artwork'] is False
    with Session(engine) as session:
        finals = list(session.query(LibraryMetadataRevisionRecord).filter_by(actor='manual_album_remap').all())
        jobs = list(session.query(JobRecord).all())
        assert len(finals) == 2
        assert {json.loads(item.tags_json)['MUSICBRAINZ_TRACKID'] for item in finals} == {
            first_track_id,
            second_track_id,
        }
        assert any(job.kind == 'artwork_enrichment' and job.release_mbid == release_id for job in jobs)
    assert slots[3]['assignable'] is False
