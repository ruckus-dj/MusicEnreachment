from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from threading import Barrier

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.external.acoustid import AcoustIdV2Adapter
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.matching.musicbrainz_mapping import select_genres
from music_ingest.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    Ambiguous,
    FixtureCase,
    FixtureProvenance,
    LiveProvenance,
    Malformed,
    MusicBrainzHttpResponse,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    NoMatch,
    RateLimited,
    RecordingCandidate,
    RecordingEvidence,
    ReleaseCandidate,
    Unavailable,
)
from music_ingest.models import Base, ProviderScheduleRecord, ProviderSnapshotRecord
from tests.support.providers import AcoustIdFixtureProvider, MusicBrainzFixtureProvider

FIXTURES = Path(__file__).parent / 'fixtures'
NOW = datetime(2026, 7, 28, tzinfo=UTC)

_RECORDING_RELEASES = {
    'c3ab18e7-e17a-4064-a352-834b67513f33': ('beatles-release', 'Let It Be', 'The Beatles', 'Let It Be', 232, 1, 12),
    '6eddd1bf-2a06-4baf-8b31-0909963345c7': (
        'clean-release',
        'We Made It',
        'Busta Rhymes feat. Linkin Park',
        'We Made It (amended version)',
        238,
        1,
        3,
    ),
    'fea273ef-bd0b-4f3a-ba7a-6d9ed240c2f5': (
        'instrumental-release',
        'We Made It',
        'Busta Rhymes feat. Linkin Park',
        'We Made It (instrumental)',
        236,
        3,
        3,
    ),
    '5eb8e3dc-7a63-4269-9abb-a7ed70a27cf4': (
        '0f481339-f7bb-40b4-ab4a-f24c1c2a7009',
        'We Made It',
        'Busta Rhymes feat. Linkin Park',
        'We Made It (album version)',
        238,
        1,
        3,
    ),
}


def _recording_releases_response(recording_id: str) -> bytes:
    release_id, release_title, artist, _, _, _, _ = _RECORDING_RELEASES[recording_id]
    return json.dumps(
        {
            'release-count': 1,
            'release-offset': 0,
            'releases': [{'id': release_id, 'title': release_title, 'artist-credit': [{'name': artist}]}],
        }
    ).encode()


def _release_browse_response(releases: bytes, count: int) -> bytes:
    return b''.join((b'{"release-count":', str(count).encode(), b',"release-offset":0,"releases":', releases, b'}'))


def _release_response(release_id: str) -> bytes:
    recording_id, (_, release_title, artist, recording_title, duration, track_number, track_total) = next(
        (recording_id, values) for recording_id, values in _RECORDING_RELEASES.items() if values[0] == release_id
    )
    return json.dumps(
        {
            'id': release_id,
            'title': release_title,
            'country': 'JP' if release_id == '0f481339-f7bb-40b4-ab4a-f24c1c2a7009' else 'US',
            'artist-credit': [{'name': artist}],
            'media': [
                {
                    'position': 1,
                    'track-count': track_total,
                    'tracks': [
                        {
                            'position': track_number,
                            'title': recording_title,
                            'length': duration * 1000,
                            'recording': {
                                'id': recording_id,
                                'title': recording_title,
                                'artist-credit': [{'name': artist}],
                            },
                        }
                    ],
                }
            ],
        }
    ).encode()


def _service(session: Session, starts: list[datetime]) -> ProviderEvidenceService:
    return ProviderEvidenceService(
        session=session,
        musicbrainz=MusicBrainzFixtureProvider(FIXTURES / 'musicbrainz'),
        acoustid=AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        wait_until=starts.append,
    )


def test_provider_evidence_when_worker_owns_transaction_rolls_back_snapshots(tmp_path: Path) -> None:
    # Given: a worker-owned session that must atomically persist its claim and provider evidence.
    session, starts = _session(tmp_path)
    service = ProviderEvidenceService(
        session=session,
        musicbrainz=MusicBrainzFixtureProvider(FIXTURES / 'musicbrainz'),
        acoustid=AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        wait_until=starts.append,
        commit_on_persist=False,
    )

    # When: provider evidence is persisted but the worker transaction is aborted.
    _ = service.lookup(ProviderEvidenceRequest('fixture', FixtureCase.SUCCESS, None, None), NOW)
    session.rollback()

    # Then: no provider snapshot escapes the worker transaction.
    assert session.scalars(select(ProviderSnapshotRecord)).all() == []


def _session(tmp_path: Path) -> tuple[Session, list[datetime]]:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "providers.db"}')
    Base.metadata.create_all(engine)
    session = Session(engine)
    session.add_all(
        [
            ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=NOW),
            ProviderScheduleRecord(provider_name='acoustid', next_start_at=NOW),
        ]
    )
    session.commit()
    return session, []


def test_musicbrainz_when_response_is_fresh_reuses_cached_snapshot_without_a_second_adapter_call(
    tmp_path: Path,
) -> None:
    # Given: a fixture-backed provider and an initially empty durable cache.
    session, starts = _session(tmp_path)
    service = _service(session, starts)
    request = ProviderEvidenceRequest('artist:fixture release:fixture', FixtureCase.SUCCESS, None, None)

    # When: the same lookup is repeated inside the 24-hour freshness window.
    first = service.lookup(request, NOW)
    second = service.lookup(request, NOW + timedelta(hours=23, minutes=59))

    # Then: the cache has one snapshot and neither provider selected a release.
    assert isinstance(first.musicbrainz.provenance, LiveProvenance)
    assert isinstance(second.musicbrainz.provenance, LiveProvenance)
    assert first.musicbrainz.provenance.state == 'fresh'
    assert second.musicbrainz.provenance.state == 'cached'
    assert second.selected_release is None
    assert len(session.scalars(select(ProviderSnapshotRecord)).all()) == 1
    assert starts == [NOW]


def test_musicbrainz_when_cache_is_stale_records_a_new_snapshot_and_reserves_a_second_start(tmp_path: Path) -> None:
    # Given: a cached result that has reached its 24-hour expiry boundary.
    session, starts = _session(tmp_path)
    service = _service(session, starts)
    request = ProviderEvidenceRequest('artist:fixture release:fixture', FixtureCase.SUCCESS, None, None)
    _ = service.lookup(request, NOW)

    # When: the lookup occurs after the freshness window.
    result = service.lookup(request, NOW + timedelta(hours=24))

    # Then: expiry is durable evidence before fresh evidence supersedes the stale snapshot.
    assert isinstance(result.musicbrainz.provenance, LiveProvenance)
    assert result.musicbrainz.provenance.state == 'fresh'
    snapshots = session.scalars(select(ProviderSnapshotRecord).order_by(ProviderSnapshotRecord.id)).all()
    assert [snapshot.state for snapshot in snapshots] == ['fresh', 'stale', 'fresh']
    assert snapshots[1].age_seconds == 24 * 60 * 60
    assert starts[1] - starts[0] >= timedelta(seconds=1)


def test_musicbrainz_when_rate_limit_or_unavailable_persists_typed_review_evidence(tmp_path: Path) -> None:
    # Given: fixture-only malformed, rate-limited, and unavailable provider outcomes.
    session, starts = _session(tmp_path)
    service = _service(session, starts)

    # When: each failure crosses the durable provider boundary.
    outcomes = tuple(
        service.lookup(ProviderEvidenceRequest(case.value, case, None, None), NOW + timedelta(seconds=index))
        for index, case in enumerate((FixtureCase.MALFORMED, FixtureCase.RATE_LIMITED, FixtureCase.UNAVAILABLE))
    )

    # Then: each stays typed, is persisted, and cannot select a release.
    assert isinstance(outcomes[0].musicbrainz, Malformed)
    assert isinstance(outcomes[1].musicbrainz, RateLimited)
    assert isinstance(outcomes[2].musicbrainz, Unavailable)
    assert all(outcome.selected_release is None for outcome in outcomes)
    assert {snapshot.outcome for snapshot in session.scalars(select(ProviderSnapshotRecord)).all()} == {
        'malformed',
        'rate_limited',
        'unavailable',
    }
    assert len(starts) == 3


def test_acoustid_when_disabled_or_enabled_only_contributes_recording_evidence(tmp_path: Path) -> None:
    # Given: one disabled request and one configured AcoustID fixture request.
    session, starts = _session(tmp_path)
    service = _service(session, starts)

    # When: each request is resolved alongside MusicBrainz fixture evidence.
    disabled = service.lookup(ProviderEvidenceRequest('one', FixtureCase.NO_MATCH, None, None), NOW)
    enabled = service.lookup(
        ProviderEvidenceRequest('two', FixtureCase.NO_MATCH, 'fixture-fingerprint', FixtureCase.SUCCESS, 241),
        NOW + timedelta(seconds=1),
    )

    # Then: AcoustID never supplies a selected release, even when its recording evidence succeeds.
    assert disabled.acoustid is not None
    assert enabled.acoustid is not None
    assert enabled.selected_release is None
    assert len(starts) == 3


def test_provider_evidence_when_force_refresh_is_requested_bypasses_fresh_provider_cache(tmp_path: Path) -> None:
    # Given: a successful provider snapshot that would normally be reused.
    session, starts = _session(tmp_path)
    service = _service(session, starts)
    request = ProviderEvidenceRequest('two', FixtureCase.NO_MATCH, 'fixture-fingerprint', FixtureCase.SUCCESS, 241)
    _ = service.lookup(request, NOW)

    # When: the same provider request explicitly asks for a fresh lookup.
    forced = service.lookup(
        ProviderEvidenceRequest('two', FixtureCase.NO_MATCH, 'fixture-fingerprint', FixtureCase.SUCCESS, 241, True),
        NOW + timedelta(seconds=1),
    )

    # Then: the provider is scheduled again instead of returning the cached snapshot.
    assert isinstance(forced.acoustid, AcoustIdMatch)
    assert starts == [NOW, NOW, NOW + timedelta(seconds=1), NOW + timedelta(seconds=1)]


def test_musicbrainz_v2_adapter_uses_the_configured_user_agent_without_network(tmp_path: Path) -> None:
    # Given: a fixture transport that captures the configured contactable User-Agent.
    calls: list[tuple[str, dict[str, str]]] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            calls.append((url, headers))
            return MusicBrainzHttpResponse(200, b'{"releases": []}')

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the public v2 adapter performs a release lookup through the injected seam.
    result = adapter.lookup(
        MusicBrainzLookupRequest('artist:fixture', FixtureCase.SUCCESS),
        NOW,
    )

    # Then: it uses the public v2 endpoint and passes the configured User-Agent without a socket.
    assert isinstance(result.provenance, LiveProvenance)
    assert result.provenance.http_status == 200
    assert calls[0][0].startswith('https://musicbrainz.org/ws/2/recording/?')
    assert calls[0][1]['User-Agent'] == 'music-ingest/1.0 (operator@example.test)'


def test_musicbrainz_v2_adapter_uses_the_configured_host_without_network() -> None:
    # Given: a fixture transport and a self-hosted MusicBrainz URL.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases": []}')

    adapter = MusicBrainzProviderAdapter(
        FixtureTransport(), 'music-ingest/1.0 (operator@example.test)', 'https://musicbrainz.internal'
    )

    # When: the adapter performs a release lookup.
    _ = adapter.lookup(MusicBrainzLookupRequest('artist:fixture', FixtureCase.SUCCESS), NOW)

    # Then: the request targets the configured server rather than the public default.
    assert calls[0].startswith('https://musicbrainz.internal/ws/2/recording/?')


def test_musicbrainz_v2_adapter_fetches_front_artwork_for_release_once() -> None:
    # Given: a MusicBrainz transport returning a verified JPEG front cover.
    calls: list[tuple[str, dict[str, str]]] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            calls.append((url, headers))
            return MusicBrainzHttpResponse(200, b'\xff\xd8\xffcover\xff\xd9')

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the adapter requests artwork for a MusicBrainz release.
    artwork = adapter.fetch_artwork('release-id')

    # Then: the Cover Art Archive front endpoint returns a release-bound JPEG candidate.
    assert artwork is not None
    assert artwork.release_id == 'release-id'
    assert artwork.format.value == 'jpg'
    assert calls[0][0] == 'https://coverartarchive.org/release/release-id/front-500'


def test_musicbrainz_v2_adapter_when_recording_id_is_known_looks_up_linked_releases() -> None:
    # Given: AcoustID has supplied a MusicBrainz recording ID.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/release/release-id?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"id":"release-id","title":"Fixture Album","media":[{"track-count":1,'
                    b'"tracks":[{"position":1,"title":"Fixture Track","recording":{"id":"recording-id",'
                    b'"title":"Fixture Track"}}]}]}',
                )
            return MusicBrainzHttpResponse(
                200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}'
            )

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the adapter resolves the recording through MusicBrainz.
    result = adapter.lookup(
        MusicBrainzLookupRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'recording-id',
            'Fixture Album',
        ),
        NOW,
    )

    # Then: it returns the linked release through the release browse endpoint.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'release-id'
    assert '/release/?' in calls[0]
    assert 'recording=recording-id' in calls[0]
    assert 'status=' not in calls[0]


def test_musicbrainz_v2_adapter_uses_release_browse_results() -> None:
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                assert 'status=' not in url
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":1,"release-offset":0,"releases":['
                    b'{"id":"official-id","title":"Official Album","status":"Official"}]}',
                )
            return MusicBrainzHttpResponse(200, b'{"id":"official-id","title":"Official Album"}')

    result = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('', FixtureCase.SUCCESS, 'recording-id'),
        NOW,
    )

    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'official-id'


def test_musicbrainz_v2_adapter_accepts_unknown_release_status_and_excludes_bootleg() -> None:
    # Given: a recording has an unclassified release and a bootleg release.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                assert 'status=' not in url
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":2,"release-offset":0,"releases":['
                    b'{"id":"unknown-id","title":"Lenin Has Risen","status":null},'
                    b'{"id":"bootleg-id","title":"Bootleg","status":"Bootleg"}]}',
                )
            return MusicBrainzHttpResponse(200, b'{"id":"unknown-id","title":"Lenin Has Risen"}')

    # When: the adapter resolves the explicit recording identity.
    result = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('', FixtureCase.SUCCESS, 'recording-id'), NOW
    )

    # Then: the null-status release supplies the candidate while bootleg remains excluded.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'unknown-id'


def test_musicbrainz_v2_adapter_enriches_recording_release_with_track_metadata() -> None:
    # Given: MusicBrainz returns the linked release first and its full media on the release endpoint.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/release/?' in url:
                body = _release_browse_response(b'[{"id":"release-id","title":"Fixture Album"}]', 1)
            else:
                body = (
                    b'{"id":"release-id","title":"Fixture Album","date":"2020-01-02",'
                    b'"genres":[{"name":"Electronic"}],"release-group":{"id":"group-id",'
                    b'"first-release-date":"2019-01-01"},"media":[{"position":1,"track-count":10,'
                    b'"tracks":[{"position":4,"title":"Fixture Track",'
                    b'"recording":{"id":"recording-id","title":"Fixture Track"}}]}]}'
                )
            return MusicBrainzHttpResponse(200, body)

    result = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'recording-id',
            'Fixture Album',
        ),
        NOW,
    )

    # Then: the candidate carries the fields required to build the published Final tags.
    assert isinstance(result, MusicBrainzMatch)
    assert len(calls) == 2
    assert result.candidate.recording_title == 'Fixture Track'
    assert result.candidate.date == '2020-01-02'
    assert result.candidate.original_date == '2019-01-01'
    assert result.candidate.track_number == 4
    assert result.candidate.track_total == 10
    assert result.candidate.genres == ('Electronic',)


def test_musicbrainz_v2_adapter_when_search_has_many_tracks_selects_title_duration_and_number_match() -> None:
    # Given: a text search returns the album, whose first track is not the source recording.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(200, b'{"recordings":[{"id":"right-id"}]}')
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200, _release_browse_response(b'[{"id":"release-id","title":"Fixture Album"}]', 1)
                )
            return MusicBrainzHttpResponse(
                200,
                b'{"id":"release-id","title":"Fixture Album","media":[{"track-count":2,"tracks":['
                b'{"position":1,"title":"Other Song","length":120000,"recording":'
                b'{"id":"wrong-id","title":"Other Song"}},'
                b'{"position":2,"title":"Target Song","length":215000,"recording":'
                b'{"id":"right-id","title":"Target Song"}}]}]}',
            )

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: MusicBrainz ranks the enriched release against the source track context.
    result = adapter.lookup(
        MusicBrainzLookupRequest(
            'artist:"Fixture Artist" release:"Fixture Album" recording:"Target Song"',
            FixtureCase.SUCCESS,
            release_title='Fixture Album',
            artist_name='Fixture Artist',
            recording_title='Target Song',
            duration_seconds=215,
            track_number=2,
        ),
        NOW,
    )

    # Then: the candidate carries the matching recording MBID, not the album's first track.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.recording_mbids == ('right-id',)
    assert result.candidate.recording_title == 'Target Song'
    assert result.candidate.track_number == 2


def test_musicbrainz_v2_adapter_recording_search_expands_all_results_before_ranking() -> None:
    # Given: a recording search contains a likely match and two lower-ranked results.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":['
                    b'{"id":"right-id","title":"Target Song","artist-credit":[{"name":"Fixture Artist"}]},'
                    b'{"id":"wrong-title","title":"Other Song","artist-credit":[{"name":"Fixture Artist"}]},'
                    b'{"id":"wrong-artist","title":"Target Song","artist-credit":[{"name":"Other Artist"}]}]}',
                )
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200, _release_browse_response(b'[{"id":"release-id","title":"Fixture Album"}]', 1)
                )
            return MusicBrainzHttpResponse(200, b'{"id":"release-id","title":"Fixture Album"}')

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: MusicBrainz expands every recording returned by the provider.
    result = adapter.lookup(
        MusicBrainzLookupRequest(
            'artist:"Fixture Artist" recording:"Target Song"',
            FixtureCase.SUCCESS,
            artist_name='Fixture Artist',
            recording_title='Target Song',
        ),
        NOW,
    )

    # Then: every returned recording is expanded into release candidates for later ranking.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.recording_mbids == ('right-id', 'wrong-title', 'wrong-artist')
    assert any('/release/?recording=wrong-title' in url for url in calls)
    assert any('/release/?recording=wrong-artist' in url for url in calls)


def test_musicbrainz_v2_adapter_recording_search_expands_independent_recordings_concurrently() -> None:
    # Given: two recording details that both must begin before either can return.
    started = Barrier(2)

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}',
                )
            if '/release/?' in url:
                started.wait(timeout=1)
                recording_id = url.split('recording=', 1)[1].split('&', 1)[0]
                return MusicBrainzHttpResponse(
                    200,
                    _release_browse_response(
                        f'[{{"id":"release-{recording_id}","title":"Fixture Album"}}]'.encode(), 1
                    ),
                )
            release_id = url.split('/release/', 1)[1].split('?', 1)[0]
            return MusicBrainzHttpResponse(200, f'{{"id":"{release_id}","title":"Fixture Album"}}'.encode())

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the text search expands its independent recording candidates.
    result = adapter.lookup(MusicBrainzLookupRequest('artist:Fixture', FixtureCase.SUCCESS), NOW)

    # Then: both recording candidates are preserved after the concurrent expansion.
    assert isinstance(result, Ambiguous)
    assert tuple(candidate.release_mbid for candidate in result.candidates) == (
        'release-recording-a',
        'release-recording-b',
    )


def test_musicbrainz_v2_adapter_preserves_release_artist_separately_from_track_artist() -> None:
    # Given: the source track artist differs from the artist credited for the matched release.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200, _release_browse_response(b'[{"id":"release-id","title":"Fixture Album"}]', 1)
                )
            return MusicBrainzHttpResponse(
                200,
                b'{"id":"release-id","title":"Fixture Album",'
                b'"artist-credit":[{"name":"Album Artist"}],'
                b'"media":[{"position":1,"tracks":[{"position":1,"title":"Fixture Track",'
                b'"recording":{"id":"recording-id","title":"Fixture Track"}}]}]}',
            )

    adapter = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the adapter resolves the recording to its release.
    result = adapter.lookup(
        MusicBrainzLookupRequest(
            'artist:Track Artist release:Fixture Album',
            FixtureCase.SUCCESS,
            'recording-id',
            'Fixture Album',
            'Track Artist',
        ),
        NOW,
    )

    # Then: matching retains the track artist while the release credit remains independently available.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.artist_name == 'Track Artist'
    assert result.candidate.release_artist_name == 'Album Artist'


def test_musicbrainz_v2_adapter_reads_genres_from_nested_artist_credit_artist() -> None:
    # Given: MusicBrainz places artist genres under artist-credit[].artist.genres.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                body = _release_browse_response(b'[{"id":"release-id","title":"Fixture Album"}]', 1)
            else:
                body = (
                    b'{"id":"release-id","title":"Fixture Album",'
                    b'"artist-credit":[{"name":"Fixture Artist","artist":{'
                    b'"id":"artist-id","name":"Fixture Artist",'
                    b'"genres":[{"name":"Alternative Rock"},{"name":"Hip Hop"}]}}],'
                    b'"media":[{"position":1,"tracks":[{"position":1,"title":"Fixture Track",'
                    b'"recording":{"id":"recording-id","title":"Fixture Track",'
                    b'"isrcs":["USFIX2600001"],"relations":['
                    b'{"type":"performer","target-type":"artist",'
                    b'"artist":{"id":"performer-id","name":"Fixture Performer"}},'
                    b'{"type":"vocal","target-type":"artist",'
                    b'"artist":{"id":"vocalist-id","name":"Fixture Vocalist"}}]}}]}]}'
                )
            return MusicBrainzHttpResponse(200, body)

    result = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('artist:Fixture', FixtureCase.SUCCESS, 'recording-id', 'Fixture Album'),
        NOW,
    )

    # Then: the candidate includes genres from the linked artist entity.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.genres == ('Alternative Rock', 'Hip Hop')
    assert result.candidate.isrcs == ('USFIX2600001',)
    assert result.candidate.performers == ('Fixture Performer', 'Fixture Vocalist')


def test_musicbrainz_genre_selection_prefers_track_then_album_then_artist() -> None:
    assert select_genres(('Track',), ('Album',), ('Artist',)) == ('Track',)
    assert select_genres((), ('Album',), ('Artist',)) == ('Album',)
    assert select_genres((), (), ('Artist',)) == ('Artist',)


def test_musicbrainz_v2_adapter_preserves_release_catalog_numbers() -> None:
    # Given: a detailed release response includes barcode and label catalog numbers.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200,
                b'{"id":"release-eu","title":"Burn It Down","barcode":"093624950509",'
                b'"disambiguation":"European edition","label-info":[{"catalog-number":"9362-49505-0"}],"media":[]}',
            )

    # When: the provider parses the detailed release.
    result = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('', FixtureCase.SUCCESS, recording_mbid='recording-eu', release_mbid='release-eu'),
        NOW,
    )

    # Then: both edition identifiers remain available to the matcher.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.catalog_numbers == ('093624950509', '9362-49505-0')
    assert result.candidate.disambiguation == 'European edition'


def test_provider_evidence_when_acoustid_is_confident_uses_recording_lookup(tmp_path: Path) -> None:
    # Given: a high-confidence AcoustID fixture and a MusicBrainz transport.
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/ws/2/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"text-recording-id","score":100}]}',
                )
            if '/release/release-id?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"id":"release-id","title":"Fixture Album","media":[{"track-count":1,'
                    b'"tracks":[{"position":1,"title":"Fixture Track","recording":{"id":"recording-id",'
                    b'"title":"Fixture Track"}}]}]}',
                )
            return MusicBrainzHttpResponse(
                200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}'
            )

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        starts.append,
    )

    # When: provider evidence is looked up for the album.
    result = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'fixture-fingerprint',
            FixtureCase.SUCCESS,
            241,
            release_title='Fixture Album',
            artist_name='Fixture Artist',
        ),
        NOW,
    )

    # Then: MusicBrainz searches text first, then expands the AcoustID recording ID.
    assert isinstance(result.musicbrainz, MusicBrainzMatch)
    assert '/ws/2/recording/?' in calls[0]
    assert any('/release/?recording=f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a' in call for call in calls)
    assert any('/release/?recording=text-recording-id' in call for call in calls)
    assert result.musicbrainz.candidate.artist_name == 'Fixture Artist'
    assert set(result.musicbrainz.candidate.recording_mbids) == {
        'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
        'text-recording-id',
    }


def test_provider_evidence_recording_only_lookup_skips_text_search(tmp_path: Path) -> None:
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(
                200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}'
            )

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        None,
        starts.append,
    )

    result = service.lookup(
        ProviderEvidenceRequest(
            '',
            FixtureCase.SUCCESS,
            None,
            None,
            recording_mbid='recording-id',
            artist_name='Fixture Artist',
        ),
        NOW,
    )

    assert isinstance(result.musicbrainz, MusicBrainzMatch)
    assert calls[0] == 'https://musicbrainz.org/ws/2/release/?recording=recording-id&fmt=json&limit=100&offset=0'
    assert len(calls) == 2
    assert all('/ws/2/recording/?' not in url for url in calls)


def test_provider_evidence_preserves_ambiguous_recording_candidates_after_persistence(tmp_path: Path) -> None:
    # Given: a recording maps to two releases with equivalent source titles.
    session, starts = _session(tmp_path)

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    _release_browse_response(
                        b'[{"id":"release-a","title":"Fixture Album","status":"Official"},'
                        b'{"id":"release-b","title":"Fixture Album","status":"Official"}]',
                        2,
                    ),
                )
            release_id = 'release-b' if '/release/release-b?' in url else 'release-a'
            return MusicBrainzHttpResponse(200, f'{{"id":"{release_id}","title":"Fixture Album","media":[]}}'.encode())

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        starts.append,
    )

    # When: the provider result crosses the durable evidence boundary.
    result = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'fixture-fingerprint',
            FixtureCase.SUCCESS,
            241,
            release_title='Fixture Album',
            artist_name='Fixture Artist',
        ),
        NOW,
    )

    # Then: persistence retains both candidates for manual review.
    assert isinstance(result.musicbrainz, Ambiguous)
    assert tuple(candidate.release_mbid for candidate in result.musicbrainz.candidates) == ('release-a', 'release-b')


def test_provider_evidence_expands_every_acoustid_recording_and_deduplicates_releases(tmp_path: Path) -> None:
    session, starts = _session(tmp_path)
    recording_ids = ('recording-a', 'recording-b')
    calls: list[str] = []

    class AcoustIdProvider:
        def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdMatch:
            _ = request, now
            return AcoustIdMatch(
                FixtureProvenance(FIXTURES / 'acoustid' / 'success.json', 'acoustid'),
                RecordingEvidence(
                    recording_ids[0],
                    0.95,
                    tuple(RecordingCandidate(recording_id, 0.9) for recording_id in recording_ids),
                ),
            )

    class MusicBrainzProvider:
        def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzMatch:
            _ = now
            calls.append('text-search')
            return MusicBrainzMatch(
                FixtureProvenance(FIXTURES / 'musicbrainz' / 'success.json', 'musicbrainz'),
                ReleaseCandidate(
                    'shared-release',
                    'Shared Album',
                    'Fixture Artist',
                    recording_mbids=request.recording_mbids,
                ),
            )

    service = ProviderEvidenceService(session, MusicBrainzProvider(), AcoustIdProvider(), starts.append)

    result = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture recording:Track',
            FixtureCase.SUCCESS,
            'fixture-fingerprint',
            FixtureCase.SUCCESS,
            241,
        ),
        NOW,
    )

    assert calls == ['text-search']
    assert isinstance(result.musicbrainz, MusicBrainzMatch)
    assert result.musicbrainz.candidate.release_mbid == 'shared-release'
    assert result.musicbrainz.candidate.recording_mbids == recording_ids


def test_musicbrainz_searches_recordings_then_expands_each_recording_to_releases() -> None:
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}',
                )
            if '/release/?recording=recording-a' in url:
                return MusicBrainzHttpResponse(
                    200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-a","title":"Album A"}]}'
                )
            if '/release/?recording=recording-b' in url:
                return MusicBrainzHttpResponse(
                    200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-b","title":"Album B"}]}'
                )
            if '/release/release-a?' in url:
                return MusicBrainzHttpResponse(200, b'{"id":"release-a","title":"Album A"}')
            if '/release/release-b?' in url:
                return MusicBrainzHttpResponse(200, b'{"id":"release-b","title":"Album B"}')
            raise AssertionError(f'unexpected MusicBrainz URL: {url}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    result = provider.lookup(
        MusicBrainzLookupRequest('artist:Fixture recording:Track', FixtureCase.SUCCESS),
        NOW,
    )

    assert isinstance(result, Ambiguous)
    assert [candidate.release_mbid for candidate in result.candidates] == ['release-a', 'release-b']
    assert [candidate.recording_mbids for candidate in result.candidates] == [('recording-a',), ('recording-b',)]


def test_musicbrainz_search_with_recordings_without_releases_returns_no_match() -> None:
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(200, b'{"recordings":[{"id":"recording-a"}]}')
            if '/release/?recording=recording-a' in url:
                return MusicBrainzHttpResponse(200, b'{"release-count":0,"release-offset":0,"releases":[]}')
            raise AssertionError(f'unexpected MusicBrainz URL: {url}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    result = provider.lookup(
        MusicBrainzLookupRequest('artist:Fixture recording:Track', FixtureCase.SUCCESS),
        NOW,
    )

    assert isinstance(result, NoMatch)


def test_musicbrainz_search_deduplicates_release_enrichment_across_recordings() -> None:
    # Given: two recording results point to the same release and one additional release.
    release_calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}',
                )
            if '/release/?recording=recording-a' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":2,"release-offset":0,"releases":[{"id":"shared-release","title":"Shared Album"},'
                    b'{"id":"release-a","title":"Album A"}]}',
                )
            if '/release/?recording=recording-b' in url:
                return MusicBrainzHttpResponse(
                    200,
                    _release_browse_response(b'[{"id":"shared-release","title":"Shared Album"}]', 1),
                )
            if '/release/' in url and '/release/?' not in url:
                release_id = url.split('/release/', 1)[1].split('?', 1)[0]
                release_calls.append(release_id)
                return MusicBrainzHttpResponse(200, f'{{"id":"{release_id}","title":"Shared Album"}}'.encode())
            raise AssertionError(f'unexpected MusicBrainz URL: {url}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the adapter resolves the recording search and its linked releases.
    result = provider.lookup(
        MusicBrainzLookupRequest('artist:Fixture recording:Track', FixtureCase.SUCCESS),
        NOW,
    )

    # Then: each release MBID is enriched once and shared-release provenance keeps both recordings.
    assert isinstance(result, Ambiguous)
    assert release_calls.count('shared-release') == 1
    assert sorted(release_calls) == ['release-a', 'shared-release']
    shared = next(candidate for candidate in result.candidates if candidate.release_mbid == 'shared-release')
    assert shared.recording_mbids == ('recording-a', 'recording-b')


def test_musicbrainz_search_preserves_recording_metadata_for_all_recording_projections() -> None:
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}',
                )
            if '/release/?recording=recording-a' in url or '/release/?recording=recording-b' in url:
                return MusicBrainzHttpResponse(
                    200,
                    _release_browse_response(b'[{"id":"shared-release","title":"Shared Album"}]', 1),
                )
            if '/release/shared-release?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"id":"shared-release","title":"Shared Album","media":[{"position":1,"tracks":['
                    b'{"position":1,"title":"Wrong Song","recording":{"id":"recording-a","title":"Wrong Song"}},'
                    b'{"position":2,"title":"Target Song","recording":{"id":"recording-b","title":"Target Song"}}'
                    b']}]}',
                )
            raise AssertionError(f'unexpected MusicBrainz URL: {url}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    result = provider.lookup(
        MusicBrainzLookupRequest(
            'artist:Fixture recording:Target',
            FixtureCase.SUCCESS,
            artist_name='Fixture Artist',
            recording_title='Target Song',
            recording_mbids=('recording-a', 'recording-b'),
        ),
        NOW,
    )

    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.recording_mbids == ('recording-a', 'recording-b')
    assert [item.recording_title for item in result.candidate.recording_candidates] == ['Wrong Song', 'Target Song']


def test_musicbrainz_search_merges_acoustid_recordings_before_detail_lookup() -> None:
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}',
                )
            if '/release/?recording=recording-a' in url:
                body = _release_browse_response(b'[{"id":"shared-release","title":"Shared Album"}]', 1)
            elif '/release/?recording=recording-b' in url:
                body = _release_browse_response(
                    b'[{"id":"shared-release","title":"Shared Album"},{"id":"release-b","title":"Album B"}]', 2
                )
            elif '/release/?recording=recording-c' in url:
                body = b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-c","title":"Album C"}]}'
            elif '/release/' in url and '/release/?' not in url:
                release_id = url.split('/release/', 1)[1].split('?', 1)[0]
                body = f'{{"id":"{release_id}","title":"{release_id}"}}'.encode()
            else:
                raise AssertionError(f'unexpected MusicBrainz URL: {url}')
            return MusicBrainzHttpResponse(200, body)

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    result = provider.lookup(
        MusicBrainzLookupRequest(
            'artist:Fixture recording:Track',
            FixtureCase.SUCCESS,
            recording_mbids=('recording-c',),
        ),
        NOW,
    )

    assert isinstance(result, Ambiguous)
    assert '/recording/?' in calls[0]
    recording_calls = [url for url in calls if '/release/?recording=' in url]
    assert {url.split('recording=', 1)[1].split('&', 1)[0] for url in recording_calls} == {
        'recording-a',
        'recording-b',
        'recording-c',
    }
    release_calls = [url for url in calls if '/release/' in url and '/release/?' not in url]
    assert sorted(url.split('/release/', 1)[1].split('?', 1)[0] for url in release_calls) == [
        'release-b',
        'release-c',
        'shared-release',
    ]


def test_provider_evidence_when_acoustid_confidence_is_low_uses_text_search_fallback(tmp_path: Path) -> None:
    # Given: AcoustID evidence below the configured confidence threshold.
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(
                200, b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}'
            )

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        starts.append,
    )

    # When: provider evidence is looked up with a threshold above the AcoustID score.
    _ = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'fixture-fingerprint',
            FixtureCase.SUCCESS,
            241,
            release_title='Fixture Album',
            acoustid_confidence_threshold=0.99,
        ),
        NOW,
    )

    # Then: the fallback uses the text release search.
    assert '/ws/2/recording/?' in calls[0]
    assert '/release/?recording=f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a' in calls[1]


def test_provider_evidence_uses_every_persisted_acoustid_recording_mbid(tmp_path: Path) -> None:
    # Given: a MusicBrainz retry has persisted multiple AcoustID recording candidates.
    session, starts = _session(tmp_path)
    recording_mbids = tuple(_RECORDING_RELEASES)[:2]
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(200, b'{"recordings": []}')
            for recording_mbid in recording_mbids:
                if f'/release/?recording={recording_mbid}' in url:
                    return MusicBrainzHttpResponse(200, _recording_releases_response(recording_mbid))
            for release_mbid, *_ in (_RECORDING_RELEASES[recording_mbid] for recording_mbid in recording_mbids):
                if f'/release/{release_mbid}?' in url:
                    return MusicBrainzHttpResponse(200, _release_response(release_mbid))
            raise AssertionError(f'unexpected MusicBrainz request: {url}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        None,
        starts.append,
    )

    # When: the retry supplies the whole persisted candidate set alongside its primary recording.
    result = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            None,
            None,
            recording_mbid=recording_mbids[0],
            recording_mbids=recording_mbids,
            run_acoustid=False,
        ),
        NOW,
    )

    # Then: every recording is resolved and all of their linked releases remain reviewable.
    assert isinstance(result.musicbrainz, Ambiguous)
    assert {candidate.release_mbid for candidate in result.musicbrainz.candidates} == {
        _RECORDING_RELEASES[recording_mbid][0] for recording_mbid in recording_mbids
    }
    assert all(
        any(f'/release/?recording={recording_mbid}' in url for url in calls) for recording_mbid in recording_mbids
    )


def test_musicbrainz_v2_adapter_when_used_by_provider_service_is_compatible_and_cached(tmp_path: Path) -> None:
    # Given: the public adapter and service share an injected, socket-free transport.
    session, starts = _session(tmp_path)

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(200, b'{"releases": []}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        None,
        starts.append,
    )
    request = ProviderEvidenceRequest('artist:fixture', FixtureCase.SUCCESS, None, None)

    # When: the public adapter is invoked through the production service seam.
    first = service.lookup(request, NOW)
    second = service.lookup(request, NOW + timedelta(minutes=1))

    # Then: no signature mismatch occurs and the second result is cached.
    assert isinstance(first.musicbrainz.provenance, LiveProvenance)
    assert isinstance(second.musicbrainz.provenance, LiveProvenance)
    assert first.musicbrainz.provenance.state == 'fresh'
    assert second.musicbrainz.provenance.state == 'cached'
    assert starts == [NOW]


def test_acoustid_v2_adapter_when_configured_produces_only_recording_evidence(tmp_path: Path) -> None:
    # Given: a configured injected transport returning one AcoustID recording.
    calls: list[tuple[str, dict[str, str]]] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            calls.append((url, headers))
            return MusicBrainzHttpResponse(
                200, b'{"status":"ok","results":[{"score":0.9,"recordings":[{"id":"recording-id"}]}]}'
            )

    adapter = AcoustIdV2Adapter(FixtureTransport(), 'fixture-client-key')

    # When: configured AcoustID evidence is obtained through its injected transport.
    result = adapter.lookup(AcoustIdLookupRequest('fixture-fingerprint', FixtureCase.SUCCESS, 241))

    # Then: it carries recording evidence rather than release-selection metadata.
    match result:
        case AcoustIdMatch(evidence=evidence):
            assert evidence.recording_mbid == 'recording-id'
        case _:
            raise AssertionError('configured AcoustID fixture did not produce recording evidence')
    assert 'client=fixture-client-key' in calls[0][0]
    assert 'duration=241' in calls[0][0]
    assert 'format=json' in calls[0][0]
    assert 'meta=recordingids' in calls[0][0]
    assert calls[0][1] == {'Accept': 'application/json'}


def test_acoustid_v2_adapter_preserves_all_recording_matches_for_manual_selection() -> None:
    # Given: AcousticID returns two MusicBrainz recordings for one fingerprint.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200,
                b'{"status":"ok","results":[{"score":0.99,"recordings":[{"id":"recording-a"},{"id":"recording-b"}]}]}',
            )

    adapter = AcoustIdV2Adapter(FixtureTransport(), 'client')

    # When: the fingerprint lookup completes.
    result = adapter.lookup(AcoustIdLookupRequest('fingerprint', FixtureCase.SUCCESS, 240), NOW)

    # Then: the UI can offer both recordings instead of silently keeping only the first.
    assert isinstance(result, AcoustIdMatch)
    assert tuple(item.recording_mbid for item in result.evidence.candidates) == ('recording-a', 'recording-b')


def test_acoustid_v2_adapter_when_a_result_has_no_recordings_keeps_valid_recording_evidence() -> None:
    # Given: the current API shape includes a useful result and a score-only result.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200, b'{"status":"ok","results":[{"score":0.99,"recordings":[{"id":"recording-id"}]},{"score":0.93}]}'
            )

    adapter = AcoustIdV2Adapter(FixtureTransport(), 'fixture-client-key')

    # When: the adapter parses the response.
    result = adapter.lookup(AcoustIdLookupRequest('fixture-fingerprint', FixtureCase.SUCCESS, 173), NOW)

    # Then: the valid first result remains usable evidence.
    assert isinstance(result, AcoustIdMatch)
    assert result.evidence.recording_mbid == 'recording-id'
