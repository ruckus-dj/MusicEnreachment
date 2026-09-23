from __future__ import annotations

import json
from datetime import UTC, datetime
from hashlib import sha256
from urllib.parse import parse_qs, urlsplit

import anyio

from music_ingest.adapters.external.musicbrainz import MusicBrainzClient
from music_ingest.services.matching.evidence import parse_musicbrainz_snapshot, serialize_musicbrainz_snapshot
from music_ingest.services.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.services.matching.providers import (
    Ambiguous,
    FixtureCase,
    LiveProvenance,
    MusicBrainzHttpResponse,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    RateLimited,
    ReleaseCandidate,
)

NOW = datetime(2026, 7, 28, tzinfo=UTC)


def test_recording_details_when_ids_repeat_deduplicates_and_fans_out() -> None:
    # Given: a fixture transport whose independent recording requests must overlap.
    started = anyio.Event()
    calls: list[str] = []

    class FixtureTransport:
        async def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if len(calls) == 2:
                started.set()
            await started.wait()
            recording_id = parse_qs(urlsplit(url).query)['recording'][0]
            return MusicBrainzHttpResponse(
                200, json.dumps({'release-count': 0, 'release-offset': 0, 'releases': [], 'id': recording_id}).encode()
            )

    async def request_details() -> tuple[str, ...]:
        client = MusicBrainzClient(FixtureTransport(), 'music-ingest/test (operator@example.test)', concurrency=2)
        details = await client.recording_details(('recording-a', 'recording-a', 'recording-b'))
        return tuple(details)

    # When: duplicate recording IDs are requested as a batch.
    recording_ids = anyio.run(request_details)

    # Then: each ID is fetched once and both independent calls can make progress.
    assert recording_ids == ('recording-a', 'recording-b')
    assert len(calls) == 2


def test_recording_details_when_releases_span_pages_collects_every_page() -> None:
    # Given: a recording whose eligible releases require two browse pages.
    calls: list[str] = []

    class FixtureTransport:
        async def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            query = parse_qs(urlsplit(url).query)
            offset = query['offset'][0]
            body = (
                b''.join(
                    (
                        b'{"release-count":3,"release-offset":0,"releases":[{"id":"release-a","title":"A"},',
                        b'{"id":"release-b","title":"B"}]}',
                    )
                )
                if offset == '0'
                else b'{"release-count":3,"release-offset":2,"releases":[{"id":"release-c","title":"C"}]}'
            )
            return MusicBrainzHttpResponse(200, body)

    async def request_details() -> tuple[str, ...]:
        client = MusicBrainzClient(FixtureTransport(), 'music-ingest/test (operator@example.test)')
        details = await client.recording_details(('recording-id',))
        payload = details['recording-id'].response.payload
        assert payload is not None
        return tuple(release.id for release in payload.releases)

    # When: the client discovers releases for the recording.
    release_ids = anyio.run(request_details)

    # Then: every release page is collected through the paginated browse endpoint.
    assert release_ids == ('release-a', 'release-b', 'release-c')
    assert len(calls) == 2
    assert all('/ws/2/release/?' in url for url in calls)
    assert all('recording=recording-id' in url and 'status=' not in url and 'limit=100' in url for url in calls)


def test_search_when_musicbrainz_returns_scores_preserves_them_on_candidates() -> None:
    # Given: a text search response with provider ranking and one recording detail.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-id","score":100,"title":"Fixture Track"}]}',
                )
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}',
                )
            return MusicBrainzHttpResponse(200, b'{"id":"release-id","title":"Fixture Album"}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/test (operator@example.test)')

    # When: the adapter resolves a recording search result into release candidates.
    result = provider.lookup(MusicBrainzLookupRequest('artist:Fixture', FixtureCase.SUCCESS), NOW)

    # Then: the candidate retains the provider score for downstream matching.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.musicbrainz_score == 100


def test_recording_lookup_when_release_is_promotion_keeps_candidate() -> None:
    # Given: the recording is present only on a promotional MusicBrainz release.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":1,"release-offset":0,"releases":'
                    + b'[{"id":"release-id","title":"Promo Single","status":"Promotion"}]}',
                )
            return MusicBrainzHttpResponse(200, b'{"id":"release-id","title":"Promo Single"}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/test (operator@example.test)')

    # When: the provider resolves the known recording into release candidates.
    result = provider.lookup(MusicBrainzLookupRequest('', FixtureCase.SUCCESS, recording_mbid='recording-id'), NOW)

    # Then: the promotional release remains available for matching and review.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'release-id'


def test_search_when_provider_returns_unit_interval_score_normalizes_to_musicbrainz_scale() -> None:
    # Given: a compatible MusicBrainz mirror returns its ranking score as 0..1.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"recordings":[{"id":"recording-id","score":0.84,"title":"Fixture Track"}]}',
                )
            if '/release/?' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"release-count":1,"release-offset":0,"releases":[{"id":"release-id","title":"Fixture Album"}]}',
                )
            return MusicBrainzHttpResponse(200, b'{"id":"release-id","title":"Fixture Album"}')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/test (operator@example.test)')

    # When: the adapter resolves the mirror's search result into a release candidate.
    result = provider.lookup(MusicBrainzLookupRequest('artist:Fixture', FixtureCase.SUCCESS), NOW)

    # Then: downstream scoring receives the same canonical 0..100 provider unit as the official API.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.musicbrainz_score == 84


def test_recording_lookup_when_rate_limited_preserves_rate_limited_outcome() -> None:
    # Given: MusicBrainz rejects a known recording lookup before any release can be resolved.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(429, b'busy')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/test (operator@example.test)')

    # When: the provider resolves an explicit recording MBID.
    result = provider.lookup(MusicBrainzLookupRequest('', FixtureCase.SUCCESS, recording_mbid='recording-id'), NOW)

    # Then: an infrastructure failure is not misrepresented as an absent match.
    assert isinstance(result, RateLimited)


def test_search_when_rate_limited_skips_known_recording_detail_requests() -> None:
    # Given: a text search is rate-limited even though AcoustID supplied a possible recording identity.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(429, b'busy')

    provider = MusicBrainzProviderAdapter(FixtureTransport(), 'music-ingest/test (operator@example.test)')

    # When: the provider receives both text context and an explicit recording candidate.
    result = provider.lookup(
        MusicBrainzLookupRequest(
            'artist:Fixture recording:Target',
            FixtureCase.SUCCESS,
            recording_mbids=('recording-id',),
        ),
        NOW,
    )

    # Then: the search outcome wins and no dependent detail request is made.
    assert isinstance(result, RateLimited)
    assert len(calls) == 1


def test_musicbrainz_snapshot_preserves_all_ambiguous_release_candidates() -> None:
    # Given: one recording lookup has two reviewable release candidates.
    result = Ambiguous(
        LiveProvenance(
            'musicbrainz',
            sha256(b'request').hexdigest(),
            sha256(b'raw-response').hexdigest(),
            200,
            NOW,
            'fresh',
            b'raw-response',
        ),
        (
            ReleaseCandidate('release-a', 'Album A', 'Artist', recording_mbids=('recording-a',)),
            ReleaseCandidate('release-b', 'Album B', 'Artist', recording_mbids=('recording-b',)),
        ),
    )

    # When: the live result is serialized for provider cache reuse.
    snapshot = parse_musicbrainz_snapshot(serialize_musicbrainz_snapshot(result))

    # Then: every release and its recording identity survives the cache boundary.
    assert snapshot is not None
    assert tuple(candidate.release_mbid for candidate in snapshot.candidates) == ('release-a', 'release-b')
    assert tuple(candidate.recording_mbids for candidate in snapshot.candidates) == (('recording-a',), ('recording-b',))
