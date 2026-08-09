from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.matching.acoustid import AcoustIdV2Adapter
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.matching.musicbrainz import MusicBrainzHttpResponse, MusicBrainzV2Adapter, select_genres
from music_ingest.matching.providers import (
    AcoustIdFixtureProvider,
    AcoustIdLookupRequest,
    AcoustIdMatch,
    Ambiguous,
    FixtureCase,
    LiveProvenance,
    Malformed,
    MusicBrainzFixtureProvider,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    RateLimited,
    Unavailable,
)
from music_ingest.persistence.models import Base, ProviderScheduleRecord, ProviderSnapshotRecord

FIXTURES = Path(__file__).parent / 'fixtures'
NOW = datetime(2026, 7, 28, tzinfo=UTC)


def _service(session: Session, starts: list[datetime]) -> ProviderEvidenceService:
    return ProviderEvidenceService(
        session=session,
        musicbrainz=MusicBrainzFixtureProvider(FIXTURES / 'musicbrainz'),
        acoustid=AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        wait_until=starts.append,
    )


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

    adapter = MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

    # When: the public v2 adapter performs a release lookup through the injected seam.
    result = adapter.lookup(
        MusicBrainzLookupRequest('artist:fixture', FixtureCase.SUCCESS),
        NOW,
    )

    # Then: it uses the public v2 endpoint and passes the configured User-Agent without a socket.
    assert isinstance(result.provenance, LiveProvenance)
    assert result.provenance.http_status == 200
    assert calls[0][0].startswith('https://musicbrainz.org/ws/2/release/?')
    assert calls[0][1]['User-Agent'] == 'music-ingest/1.0 (operator@example.test)'


def test_musicbrainz_v2_adapter_when_recording_id_is_known_looks_up_linked_releases() -> None:
    # Given: AcoustID has supplied a MusicBrainz recording ID.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases":[{"id":"release-id","title":"Fixture Album"}]}')

    adapter = MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)')

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

    # Then: it returns the linked release and uses the recording lookup endpoint.
    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'release-id'
    assert '/recording/recording-id?' in calls[0]
    assert 'inc=releases' in calls[0]


def test_musicbrainz_v2_adapter_excludes_pseudo_releases_from_recording_results() -> None:
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200,
                b'{"releases":['
                b'{"id":"pseudo-id","title":"Translated Album","status":"Pseudo-Release"},'
                b'{"id":"official-id","title":"Official Album","status":"Official"}]}',
            )

    result = MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('', FixtureCase.SUCCESS, 'recording-id'),
        NOW,
    )

    assert isinstance(result, MusicBrainzMatch)
    assert result.candidate.release_mbid == 'official-id'


def test_musicbrainz_v2_adapter_enriches_recording_release_with_track_metadata() -> None:
    # Given: MusicBrainz returns the linked release first and its full media on the release endpoint.
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            if '/recording/' in url:
                body = b'{"releases":[{"id":"release-id","title":"Fixture Album"}]}'
            else:
                body = (
                    b'{"id":"release-id","title":"Fixture Album","date":"2020-01-02",'
                    b'"genres":[{"name":"Electronic"}],"release-group":{"id":"group-id",'
                    b'"first-release-date":"2019-01-01"},"media":[{"position":1,"track-count":10,'
                    b'"tracks":[{"position":4,"title":"Fixture Track",'
                    b'"recording":{"id":"recording-id","title":"Fixture Track"}}]}]}'
                )
            return MusicBrainzHttpResponse(200, body)

    result = MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
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


def test_musicbrainz_genre_selection_prefers_track_then_album_then_artist() -> None:
    assert select_genres(('Track',), ('Album',), ('Artist',)) == ('Track',)
    assert select_genres((), ('Album',), ('Artist',)) == ('Album',)
    assert select_genres((), (), ('Artist',)) == ('Artist',)


def test_musicbrainz_v2_adapter_keeps_ambiguous_release_candidates() -> None:
    # Given: a text search returns multiple release candidates.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200,
                b'{"releases":[{"id":"release-a","title":"Album A"},{"id":"release-b","title":"Album B"}]}',
            )

    result = MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)').lookup(
        MusicBrainzLookupRequest('artist:Fixture', FixtureCase.SUCCESS),
        NOW,
    )

    # Then: review can display both selectable releases instead of losing them in Ambiguous.
    assert isinstance(result, Ambiguous)
    assert tuple(candidate.release_mbid for candidate in result.candidates) == ('release-a', 'release-b')


def test_provider_evidence_when_acoustid_is_confident_uses_recording_lookup(tmp_path: Path) -> None:
    # Given: a high-confidence AcoustID fixture and a MusicBrainz transport.
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases":[{"id":"release-id","title":"Fixture Album"}]}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
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

    # Then: MusicBrainz receives the AcoustID recording ID instead of a text search.
    assert isinstance(result.musicbrainz, MusicBrainzMatch)
    assert '/recording/f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a?' in calls[0]
    assert result.musicbrainz.candidate.artist_name == 'Fixture Artist'


def test_provider_evidence_preserves_ambiguous_recording_candidates_after_persistence(tmp_path: Path) -> None:
    # Given: a recording maps to two releases with equivalent source titles.
    session, starts = _session(tmp_path)

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            if '/recording/' in url:
                return MusicBrainzHttpResponse(
                    200,
                    b'{"releases":[{"id":"release-a","title":"Fixture Album","status":"Official"},'
                    b'{"id":"release-b","title":"Fixture Album","status":"Official"}]}',
                )
            release_id = 'release-b' if '/release/release-b?' in url else 'release-a'
            return MusicBrainzHttpResponse(200, f'{{"id":"{release_id}","title":"Fixture Album","media":[]}}'.encode())

    service = ProviderEvidenceService(
        session,
        MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
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


def test_provider_evidence_when_acoustid_confidence_is_low_uses_text_search_fallback(tmp_path: Path) -> None:
    # Given: AcoustID evidence below the configured confidence threshold.
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases":[{"id":"release-id","title":"Fixture Album"}]}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
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
    assert '/ws/2/release/?' in calls[0]


def test_provider_evidence_when_acoustid_has_no_match_uses_text_search_fallback(tmp_path: Path) -> None:
    # Given: AcoustID returns no recording for the fingerprint.
    session, starts = _session(tmp_path)
    calls: list[str] = []

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases":[{"id":"release-id","title":"Fixture Album"}]}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
        AcoustIdFixtureProvider(FIXTURES / 'acoustid'),
        starts.append,
    )

    # When: provider evidence is looked up with an AcoustID no-match fixture.
    _ = service.lookup(
        ProviderEvidenceRequest(
            'artist:Fixture release:Fixture Album',
            FixtureCase.SUCCESS,
            'fixture-fingerprint',
            FixtureCase.NO_MATCH,
            241,
            release_title='Fixture Album',
        ),
        NOW,
    )

    # Then: the fallback uses the text release search.
    assert '/ws/2/release/?' in calls[0]


def test_musicbrainz_v2_adapter_when_used_by_provider_service_is_compatible_and_cached(tmp_path: Path) -> None:
    # Given: the public adapter and service share an injected, socket-free transport.
    session, starts = _session(tmp_path)

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(200, b'{"releases": []}')

    service = ProviderEvidenceService(
        session,
        MusicBrainzV2Adapter(FixtureTransport(), 'music-ingest/1.0 (operator@example.test)'),
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
