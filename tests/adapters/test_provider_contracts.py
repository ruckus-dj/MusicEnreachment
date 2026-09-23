from __future__ import annotations

import dataclasses
import socket
from datetime import UTC, datetime, timedelta, timezone
from hashlib import sha256
from typing import assert_never

import pytest
import requests

from music_ingest.services.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    FixtureCase,
    InvalidProvenanceError,
    LiveProvenance,
    LiveTransport,
    Malformed,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    ProvenanceState,
    ProviderName,
    RateLimited,
    ReleaseCandidate,
    Timeout,
    Unavailable,
    build_live_transport,
)
from tests.support.paths import FIXTURES_DIRECTORY
from tests.support.providers import AcoustIdFixtureProvider, MusicBrainzFixtureProvider

FIXTURE_DIRECTORY = FIXTURES_DIRECTORY

FORBIDDEN_PROVENANCE_FIELDS = frozenset(
    {
        'body',
        'content',
        'raw',
        'response_bytes',
        'payload',
        'user_agent',
        'contact',
        'secret',
        'credential',
        'api_key',
        'apikey',
        'token',
        'password',
        'fingerprint',
        'authorization',
    }
)


@pytest.mark.parametrize('fixture_case', tuple(FixtureCase))
def test_musicbrainz_fixture_provider_when_fixture_case_is_requested_returns_typed_outcome(
    fixture_case: FixtureCase,
) -> None:
    # Given: a raw checked-in MusicBrainz fixture selected by an explicit case.
    provider = MusicBrainzFixtureProvider(FIXTURE_DIRECTORY / 'musicbrainz')
    request = MusicBrainzLookupRequest(query='fixture lookup', fixture_case=fixture_case)

    # When: metadata authority reads its fixture bytes from disk.
    result = provider.lookup(request)

    # Then: every fixture outcome remains typed and carries its exact-byte hash.
    assert outcome_name(result) == fixture_case.value
    assert (
        result.provenance.sha256
        == sha256((FIXTURE_DIRECTORY / 'musicbrainz' / f'{fixture_case.value}.json').read_bytes()).hexdigest()
    )


@pytest.mark.parametrize('fixture_case', tuple(FixtureCase))
def test_acoustid_fixture_provider_when_fixture_case_is_requested_returns_evidence_only_outcome(
    fixture_case: FixtureCase,
) -> None:
    # Given: a raw checked-in AcoustID fixture selected by an explicit case.
    provider = AcoustIdFixtureProvider(FIXTURE_DIRECTORY / 'acoustid')
    request = AcoustIdLookupRequest(fingerprint='fixture-fingerprint', fixture_case=fixture_case, duration_seconds=241)

    # When: evidence-only matching reads its fixture bytes from disk.
    result = provider.lookup(request)

    # Then: the result has no release selection or canonical metadata surface.
    assert outcome_name(result) == fixture_case.value
    assert (
        result.provenance.sha256
        == sha256((FIXTURE_DIRECTORY / 'acoustid' / f'{fixture_case.value}.json').read_bytes()).hexdigest()
    )
    match result:
        case AcoustIdMatch(evidence=evidence):
            assert evidence.recording_mbid == 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
        case NoMatch() | Ambiguous() | Timeout() | Malformed() | RateLimited() | Unavailable() | Disabled():
            pass
        case unreachable:
            assert_never(unreachable)


def test_musicbrainz_fixture_provider_when_success_returns_authoritative_metadata() -> None:
    # Given: the success fixture for the metadata authority.
    provider = MusicBrainzFixtureProvider(FIXTURE_DIRECTORY / 'musicbrainz')

    # When: the provider performs its fixture lookup.
    result = provider.lookup(MusicBrainzLookupRequest(query='fixture lookup', fixture_case=FixtureCase.SUCCESS))

    # Then: only MusicBrainz returns a release candidate with canonical facts.
    match result:
        case MusicBrainzMatch(candidate=candidate):
            assert candidate.release_mbid == '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'
            assert candidate.release_title == 'Fixture Release'
        case NoMatch() | Ambiguous() | Timeout() | Malformed() | RateLimited() | Unavailable() | Disabled():
            pytest.fail('success fixture did not produce metadata authority result')
        case unreachable:
            assert_never(unreachable)


def test_fixture_provider_when_raw_payload_is_malformed_returns_typed_malformed_outcome() -> None:
    # Given: invalid JSON bytes checked into the MusicBrainz malformed fixture.
    provider = MusicBrainzFixtureProvider(FIXTURE_DIRECTORY / 'musicbrainz')

    # When: the provider parses the fixture.
    result = provider.lookup(MusicBrainzLookupRequest(query='fixture lookup', fixture_case=FixtureCase.MALFORMED))

    # Then: parsing does not leak an exception beyond the provider boundary.
    assert isinstance(result, Malformed)


def test_socket_access_when_not_live_is_denied() -> None:
    # Given: the default non-live suite socket-denial fixture is active.
    # When: direct socket creation attempts a connection.
    # Then: test egress is blocked before any network operation.
    with pytest.raises(RuntimeError, match='network access denied'):
        _ = socket.create_connection(('127.0.0.1', 9), timeout=0.01)


@pytest.mark.parametrize('method_name', ('connect', 'connect_ex'))
def test_socket_connect_paths_when_not_live_are_denied(method_name: str) -> None:
    # Given: the default non-live suite socket-denial fixture is active.
    socket_instance = socket.socket()

    # When: either direct socket connection path is attempted.
    # Then: neither path can open a network connection.
    with socket_instance, pytest.raises(RuntimeError, match='network access denied'):
        getattr(socket_instance, method_name)(('127.0.0.1', 9))


def test_live_provenance_when_constructed_is_immutable_sanitized_and_retains_sha256_contract() -> None:
    # Given: a durable live provenance built only from sanitized descriptors.
    response_sha256 = sha256(b'sanitized-response').hexdigest()
    provenance = LiveProvenance(
        provider_name='musicbrainz',
        request_hash=sha256(b'sanitized-request').hexdigest(),
        sha256=response_sha256,
        http_status=200,
        captured_at=datetime(2026, 7, 28, tzinfo=UTC),
        state='fresh',
    )

    # When: the boundary inspects the provenance record.
    field_names = frozenset(field.name for field in dataclasses.fields(provenance))

    # Then: it retains the exact-hash contract and carries no raw or secret material.
    assert provenance.sha256 == response_sha256
    assert not (field_names & FORBIDDEN_PROVENANCE_FIELDS)
    with pytest.raises(dataclasses.FrozenInstanceError):
        frozen_field = 'provider_name'
        setattr(provenance, frozen_field, 'acoustid')


def test_result_when_carrying_live_provenance_preserves_typed_outcome_and_hash() -> None:
    # Given: a later Task 7 adapter attaches durable sanitized provenance to a result.
    provenance = LiveProvenance(
        provider_name='musicbrainz',
        request_hash=sha256(b'req').hexdigest(),
        sha256=sha256(b'resp').hexdigest(),
        http_status=200,
        captured_at=datetime(2026, 7, 28, tzinfo=UTC),
        state='fresh',
    )
    candidate = ReleaseCandidate('mbid', 'title', 'artist')

    # When: the result exposes provenance through the shared contract.
    result: MusicBrainzResult = MusicBrainzMatch(provenance, candidate)

    # Then: the same .sha256 contract that fixture tests use still holds.
    assert result.provenance.sha256 == provenance.sha256


_BAD_DIGESTS = [
    'SANITIZED',
    sha256(b'x').hexdigest().upper(),
    sha256(b'x').hexdigest()[:63],
    sha256(b'x').hexdigest() + 'a',
    'g' * 64,
    '',
    'sk-live-0000000000000000000000000000000000000000000000000000secret',
]


def _live_provenance(
    *,
    provider_name: str = 'musicbrainz',
    request_hash: str | None = None,
    sha256_digest: str | None = None,
    http_status: int | None = 200,
    captured_at: datetime | None = None,
    state: str = 'fresh',
) -> LiveProvenance:
    return LiveProvenance(
        provider_name=provider_name,
        request_hash=sha256(b'sanitized-request').hexdigest() if request_hash is None else request_hash,
        sha256=sha256(b'sanitized-response').hexdigest() if sha256_digest is None else sha256_digest,
        http_status=http_status,
        captured_at=datetime(2026, 7, 28, tzinfo=UTC) if captured_at is None else captured_at,
        state=state,
    )


@pytest.mark.parametrize('bad_digest', _BAD_DIGESTS)
def test_live_provenance_when_request_hash_is_not_lowercase_sha256_is_rejected(bad_digest: str) -> None:
    # Given: a provenance whose request-hash digest carries a malformed or secret-bearing value.
    # When: construction validates the digest at the boundary.
    # Then: the boundary rejects it and never leaks the untrusted value.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = _live_provenance(request_hash=bad_digest)
    assert not bad_digest or bad_digest not in str(raised.value)


@pytest.mark.parametrize('bad_digest', _BAD_DIGESTS)
def test_live_provenance_when_response_sha256_is_not_lowercase_sha256_is_rejected(bad_digest: str) -> None:
    # Given: a provenance whose response digest carries a malformed or secret-bearing value.
    # When: construction validates the digest at the boundary.
    # Then: the boundary rejects it and never leaks the untrusted value.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = _live_provenance(sha256_digest=bad_digest)
    assert not bad_digest or bad_digest not in str(raised.value)


@pytest.mark.parametrize('bad_provider', ['spotify', 'MusicBrainz', 'musicbrainz\n', 'secret-marker', ''])
def test_live_provenance_when_provider_is_not_allowlisted_is_rejected(bad_provider: str) -> None:
    # Given: a provenance naming a provider outside the known allowlist.
    # When: construction checks the provider identity.
    # Then: only known provider identities are accepted and the value never leaks.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = _live_provenance(provider_name=bad_provider)
    assert not bad_provider or bad_provider not in str(raised.value)


@pytest.mark.parametrize('bad_state', ['expired', 'FRESH', 'secret-state', 'fresh\x00', ''])
def test_live_provenance_when_state_is_outside_safe_set_is_rejected(bad_state: str) -> None:
    # Given: a provenance carrying a cache-origin state outside the finite safe set.
    # When: construction checks the provenance state.
    # Then: only the finite safe state set is accepted and the value never leaks.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = _live_provenance(state=bad_state)
    assert not bad_state or bad_state not in str(raised.value)


@pytest.mark.parametrize('bad_status', [0, 99, 600, 1000, -200])
def test_live_provenance_when_http_status_is_out_of_range_is_rejected(bad_status: int) -> None:
    # Given: a provenance carrying an HTTP status outside the valid response range.
    # When: construction validates the status range.
    # Then: the boundary rejects statuses outside 100-599.
    with pytest.raises(InvalidProvenanceError):
        _ = _live_provenance(http_status=bad_status)


def test_live_provenance_when_http_status_is_absent_is_accepted() -> None:
    # Given: a provenance for a request that produced no HTTP response (timeout).
    # When: construction validates the optional status.
    # Then: a missing status is a valid timeout/no-response provenance.
    provenance = _live_provenance(http_status=None)
    assert provenance.http_status is None


@pytest.mark.parametrize(
    'bad_capture',
    [
        datetime(2026, 7, 28),  # noqa: DTZ001 - intentionally naive to prove rejection
        datetime(2026, 7, 28, tzinfo=timezone(timedelta(hours=2))),
    ],
)
def test_live_provenance_when_capture_time_is_not_utc_is_rejected(bad_capture: datetime) -> None:
    # Given: a provenance whose capture time is naive or offset from UTC.
    # When: construction validates the capture instant.
    # Then: only UTC capture instants are accepted.
    with pytest.raises(InvalidProvenanceError):
        _ = _live_provenance(captured_at=bad_capture)


def test_live_provenance_when_constructed_from_secret_bearing_input_never_leaks_it_in_repr() -> None:
    # Given: a synthetic non-secret marker mimicking secret-bearing external text.
    marker = 'adversarial-provenance-sentinel'

    # When: the boundary rejects the unsafe digest.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = _live_provenance(request_hash=marker)

    # Then: neither the raised error nor a valid provenance repr surfaces the marker.
    assert marker not in str(raised.value)
    safe = _live_provenance()
    assert marker not in repr(safe)


def test_live_provenance_when_http_status_is_a_float_is_rejected() -> None:
    # Given: a valid provenance whose HTTP status is replaced by a non-int float.
    # When: construction re-validates the status runtime type.
    # Then: a float status is rejected through the typed boundary error.
    with pytest.raises(InvalidProvenanceError):
        _ = dataclasses.replace(_live_provenance(), http_status=200.0)


def test_live_provenance_when_request_hash_is_wrong_runtime_type_is_rejected() -> None:
    # Given: a valid provenance whose request_hash is replaced by a non-str value.
    # When: construction validates the digest runtime type before regex.
    # Then: the boundary raises the typed error, not a raw TypeError.
    with pytest.raises(InvalidProvenanceError):
        _ = dataclasses.replace(_live_provenance(), request_hash=None)


def test_live_provenance_when_http_status_is_wrong_runtime_type_is_rejected() -> None:
    # Given: a valid provenance whose HTTP status is replaced by synthetic text.
    marker = 'adversarial-provenance-sentinel'

    # When: construction validates the status runtime type before the range check.
    # Then: the boundary raises the typed error and never leaks the synthetic text.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = dataclasses.replace(_live_provenance(), http_status=marker)
    assert marker not in str(raised.value)


def test_live_provenance_when_captured_at_is_wrong_runtime_type_is_rejected() -> None:
    # Given: a valid provenance whose capture instant is replaced by synthetic text.
    marker = 'adversarial-provenance-sentinel'

    # When: construction validates the capture runtime type before utcoffset.
    # Then: the boundary raises the typed error, not a raw AttributeError, and never leaks the text.
    with pytest.raises(InvalidProvenanceError) as raised:
        _ = dataclasses.replace(_live_provenance(), captured_at=marker)
    assert marker not in str(raised.value)


def test_provider_name_and_provenance_state_enums_are_closed_allowlists() -> None:
    # Given: the canonical provider and cache-origin allowlists exposed for adapters.
    # When: an unknown identity or state is parsed through the enum.
    # Then: the enum rejects it, proving the allowlist is closed.
    assert {member.value for member in ProviderName} == {'musicbrainz', 'acoustid'}
    assert {member.value for member in ProvenanceState} == {'fresh', 'cached', 'stale'}
    with pytest.raises(ValueError, match='spotify'):
        _ = ProviderName('spotify')
    with pytest.raises(ValueError, match='expired'):
        _ = ProvenanceState('expired')


def test_production_transport_constructs_via_injected_seam() -> None:
    # Given: an injected offline client seam.

    class _RecordingClient:
        def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response:
            _ = url, headers, timeout
            raise AssertionError('the injected client is not used during construction')

        def close(self) -> None:
            return None

    injected_client = _RecordingClient()

    # When: production transport is constructed through the seam.
    transport = build_live_transport(client_factory=lambda: injected_client)

    # Then: the transport holds the injected client and never touches the network.
    assert isinstance(transport, LiveTransport)
    assert transport.client is injected_client


def test_live_transport_when_tls_request_fails_does_not_fabricate_http_503() -> None:
    # Given: a live client that cannot complete the TLS request.
    class _FailingClient:
        def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response:
            _ = url, headers, timeout
            raise requests.ConnectionError('TLS handshake failed')

        def close(self) -> None:
            return None

    # When: the transport crosses the network boundary.
    response = LiveTransport(_FailingClient()).get('https://musicbrainz.org/ws/2/release/', headers={})

    # Then: no HTTP status is claimed when no HTTP response existed.
    assert response.status_code is None
    assert response.body == b''


def test_live_transport_when_musicbrainz_is_requested_acquires_rate_limit_for_each_wire_call() -> None:
    # Given: a transport with a limiter seam and one successful offline response.
    class _RecordingLimiter:
        def __init__(self) -> None:
            self.providers: list[str] = []

        def wait(self, provider_name: str) -> None:
            self.providers.append(provider_name)

    class _SuccessfulClient:
        def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response:
            _ = url, headers, timeout
            response = requests.Response()
            response.status_code = 200
            response._content = b'{}'
            return response

        def close(self) -> None:
            return None

    limiter = _RecordingLimiter()
    transport = LiveTransport(_SuccessfulClient(), limiter=limiter)

    # When: two MusicBrainz requests, one AcoustID request, and one unrelated request cross the seam.
    _ = transport.get('https://musicbrainz.org/ws/2/release/one', headers={})
    _ = transport.get('https://musicbrainz.org/ws/2/release/two', headers={})
    _ = transport.get('https://api.acoustid.org/v2/lookup', headers={})
    _ = transport.get('https://coverartarchive.org/release/one/front-500', headers={})

    # Then: each external metadata provider reserves its own persisted schedule.
    assert limiter.providers == ['musicbrainz', 'musicbrainz', 'acoustid']


def outcome_name(result: MusicBrainzResult | AcoustIdResult) -> str:
    match result:
        case MusicBrainzMatch() | AcoustIdMatch():
            return 'success'
        case NoMatch():
            return 'no_match'
        case Ambiguous():
            return 'ambiguous'
        case Timeout():
            return 'timeout'
        case Malformed():
            return 'malformed'
        case RateLimited():
            return 'rate_limited'
        case Unavailable():
            return 'unavailable'
        case Disabled():
            return 'disabled'
        case unreachable:
            assert_never(unreachable)
