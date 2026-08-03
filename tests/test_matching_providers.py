from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.matching.acoustid import AcoustIdV2Adapter
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.matching.musicbrainz import MusicBrainzHttpResponse, MusicBrainzV2Adapter
from music_ingest.matching.providers import (
    AcoustIdFixtureProvider,
    AcoustIdLookupRequest,
    AcoustIdMatch,
    FixtureCase,
    Malformed,
    MusicBrainzFixtureProvider,
    MusicBrainzLookupRequest,
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
        ProviderEvidenceRequest('two', FixtureCase.NO_MATCH, 'fixture-fingerprint', FixtureCase.SUCCESS),
        NOW + timedelta(seconds=1),
    )

    # Then: AcoustID never supplies a selected release, even when its recording evidence succeeds.
    assert disabled.acoustid is not None
    assert enabled.acoustid is not None
    assert enabled.selected_release is None
    assert len(starts) == 3


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
    assert result.provenance.http_status == 200
    assert calls[0][0].startswith('https://musicbrainz.org/ws/2/release/?')
    assert calls[0][1]['User-Agent'] == 'music-ingest/1.0 (operator@example.test)'


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
    assert first.musicbrainz.provenance.state == 'fresh'
    assert second.musicbrainz.provenance.state == 'cached'
    assert starts == [NOW]


def test_acoustid_v2_adapter_when_configured_produces_only_recording_evidence(tmp_path: Path) -> None:
    # Given: a configured injected transport returning one AcoustID recording.
    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(
                200, b'{"status":"ok","results":[{"score":0.9,"recordings":[{"id":"recording-id"}]}]}'
            )

    adapter = AcoustIdV2Adapter(FixtureTransport(), 'fixture-client-key')

    # When: configured AcoustID evidence is obtained through its injected transport.
    result = adapter.lookup(AcoustIdLookupRequest('fixture-fingerprint', FixtureCase.SUCCESS), NOW)

    # Then: it carries recording evidence rather than release-selection metadata.
    match result:
        case AcoustIdMatch(evidence=evidence):
            assert evidence.recording_mbid == 'recording-id'
        case _:
            raise AssertionError('configured AcoustID fixture did not produce recording evidence')
