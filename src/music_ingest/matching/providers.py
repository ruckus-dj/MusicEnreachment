from __future__ import annotations

import os
import re
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum
from hashlib import sha256
from pathlib import Path
from typing import ClassVar, Final, Protocol, override

import requests
from pydantic import BaseModel, ConfigDict, ValidationError

LIVE_TRANSPORT_ENVIRONMENT: Final = 'MUSIC_INGEST_ENABLE_LIVE_TRANSPORT'
SHA256_HEX_PATTERN: Final = re.compile(r'^[0-9a-f]{64}$')
HTTP_STATUS_MINIMUM: Final = 100
HTTP_STATUS_MAXIMUM: Final = 599


class FixtureCase(StrEnum):
    SUCCESS = 'success'
    NO_MATCH = 'no_match'
    AMBIGUOUS = 'ambiguous'
    TIMEOUT = 'timeout'
    MALFORMED = 'malformed'
    RATE_LIMITED = 'rate_limited'
    UNAVAILABLE = 'unavailable'
    DISABLED = 'disabled'


class ProviderName(StrEnum):
    MUSICBRAINZ = 'musicbrainz'
    ACOUSTID = 'acoustid'


class ProvenanceState(StrEnum):
    FRESH = 'fresh'
    CACHED = 'cached'
    STALE = 'stale'


_PROVIDER_NAMES: Final = frozenset(ProviderName)
_PROVENANCE_STATES: Final = frozenset(ProvenanceState)


@dataclass(frozen=True, slots=True)
class InvalidProvenanceError(Exception):
    field_name: str
    reason: str

    @override
    def __str__(self) -> str:
        return f'live provenance {self.field_name} is invalid: {self.reason}'


def _require_str(field_name: str, value: object) -> str:
    if not isinstance(value, str):
        raise InvalidProvenanceError(field_name, 'must be a string')
    return value


def _validate_optional_http_status(value: object) -> None:
    if value is None:
        return
    if not isinstance(value, int) or isinstance(value, bool):
        raise InvalidProvenanceError('http_status', 'must be an integer or absent')
    if not (HTTP_STATUS_MINIMUM <= value <= HTTP_STATUS_MAXIMUM):
        raise InvalidProvenanceError('http_status', 'outside the valid HTTP response range')


def _validate_utc_datetime(value: object) -> None:
    if not isinstance(value, datetime):
        raise InvalidProvenanceError('captured_at', 'must be a datetime')
    if value.utcoffset() != timedelta(0):
        raise InvalidProvenanceError('captured_at', 'must be a UTC-aware instant')


@dataclass(frozen=True, slots=True)
class FixtureProvenance:
    path: Path
    sha256: str


@dataclass(frozen=True, slots=True)
class MusicBrainzHttpResponse:
    status_code: int
    body: bytes


@dataclass(frozen=True, slots=True)
class LiveProvenance:
    provider_name: str
    request_hash: str
    sha256: str
    http_status: int | None
    captured_at: datetime
    state: str
    response_body: bytes = b''

    def __post_init__(self) -> None:
        provider_name = _require_str('provider_name', self.provider_name)
        if provider_name not in _PROVIDER_NAMES:
            raise InvalidProvenanceError('provider_name', 'not an allowlisted provider identity')
        state = _require_str('state', self.state)
        if state not in _PROVENANCE_STATES:
            raise InvalidProvenanceError('state', 'not a supported cache-origin state')
        request_hash = _require_str('request_hash', self.request_hash)
        if SHA256_HEX_PATTERN.fullmatch(request_hash) is None:
            raise InvalidProvenanceError('request_hash', 'not a 64-character lowercase SHA-256 digest')
        response_hash = _require_str('sha256', self.sha256)
        if SHA256_HEX_PATTERN.fullmatch(response_hash) is None:
            raise InvalidProvenanceError('sha256', 'not a 64-character lowercase SHA-256 digest')
        _validate_optional_http_status(self.http_status)
        _validate_utc_datetime(self.captured_at)


type Provenance = FixtureProvenance | LiveProvenance


@dataclass(frozen=True, slots=True)
class MusicBrainzLookupRequest:
    query: str
    fixture_case: FixtureCase


@dataclass(frozen=True, slots=True)
class AcoustIdLookupRequest:
    fingerprint: str
    fixture_case: FixtureCase
    duration_seconds: float | None = None


@dataclass(frozen=True, slots=True)
class ReleaseCandidate:
    release_mbid: str
    release_title: str
    artist_name: str
    duration_seconds: int | None = None
    recording_mbids: tuple[str, ...] = ()
    track_mbids: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class RecordingEvidence:
    recording_mbid: str
    score: float


@dataclass(frozen=True, slots=True)
class MusicBrainzMatch:
    provenance: Provenance
    candidate: ReleaseCandidate


@dataclass(frozen=True, slots=True)
class AcoustIdMatch:
    provenance: Provenance
    evidence: RecordingEvidence


@dataclass(frozen=True, slots=True)
class NoMatch:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class Ambiguous:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class Timeout:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class Malformed:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class RateLimited:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class Unavailable:
    provenance: Provenance


@dataclass(frozen=True, slots=True)
class Disabled:
    provenance: Provenance


type MusicBrainzResult = (
    MusicBrainzMatch | NoMatch | Ambiguous | Timeout | Malformed | RateLimited | Unavailable | Disabled
)
type AcoustIdResult = AcoustIdMatch | NoMatch | Ambiguous | Timeout | Malformed | RateLimited | Unavailable | Disabled
type FixtureOutcome = NoMatch | Ambiguous | Timeout | Malformed | RateLimited | Unavailable | Disabled


class MusicBrainzProvider(Protocol):
    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult: ...


class AcoustIdProvider(Protocol):
    def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdResult: ...


class _FixturePayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    outcome: FixtureCase
    release_mbid: str | None = None
    release_title: str | None = None
    artist_name: str | None = None
    recording_mbids: tuple[str, ...] = ()
    track_mbids: tuple[str, ...] = ()
    recording_mbid: str | None = None
    score: float | None = None
    candidate_count: int | None = None


@dataclass(frozen=True, slots=True)
class MusicBrainzFixtureProvider:
    fixture_directory: Path

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        _ = now
        payload, provenance = _fixture_payload(self.fixture_directory, request.fixture_case)
        match payload:
            case None:
                return Malformed(provenance)
            case _FixturePayload(outcome=FixtureCase.SUCCESS):
                match payload.release_mbid, payload.release_title, payload.artist_name:
                    case str() as release_mbid, str() as release_title, str() as artist_name:
                        return MusicBrainzMatch(
                            provenance,
                            ReleaseCandidate(
                                release_mbid,
                                release_title,
                                artist_name,
                                recording_mbids=payload.recording_mbids,
                                track_mbids=payload.track_mbids,
                            ),
                        )
                    case _:
                        return Malformed(provenance)
            case _FixturePayload(outcome=outcome):
                return _fixture_outcome(outcome, provenance)


@dataclass(frozen=True, slots=True)
class AcoustIdFixtureProvider:
    fixture_directory: Path

    def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdResult:
        _ = now
        payload, provenance = _fixture_payload(self.fixture_directory, request.fixture_case)
        match payload:
            case None:
                return Malformed(provenance)
            case _FixturePayload(outcome=FixtureCase.SUCCESS):
                match payload.recording_mbid, payload.score:
                    case str() as recording_mbid, float() as score:
                        return AcoustIdMatch(provenance, RecordingEvidence(recording_mbid, score))
                    case _:
                        return Malformed(provenance)
            case _FixturePayload(outcome=outcome):
                return _fixture_outcome(outcome, provenance)


def _fixture_payload(
    fixture_directory: Path, fixture_case: FixtureCase
) -> tuple[_FixturePayload | None, FixtureProvenance]:
    fixture_path = fixture_directory / f'{fixture_case.value}.json'
    raw_fixture = fixture_path.read_bytes()
    provenance = FixtureProvenance(path=fixture_path, sha256=sha256(raw_fixture).hexdigest())
    try:
        return _FixturePayload.model_validate_json(raw_fixture), provenance
    except ValidationError:
        return None, provenance


def _fixture_outcome(fixture_case: FixtureCase, provenance: Provenance) -> FixtureOutcome:
    match fixture_case:
        case FixtureCase.NO_MATCH:
            return NoMatch(provenance)
        case FixtureCase.AMBIGUOUS:
            return Ambiguous(provenance)
        case FixtureCase.TIMEOUT:
            return Timeout(provenance)
        case FixtureCase.MALFORMED:
            return Malformed(provenance)
        case FixtureCase.RATE_LIMITED:
            return RateLimited(provenance)
        case FixtureCase.UNAVAILABLE:
            return Unavailable(provenance)
        case FixtureCase.DISABLED:
            return Disabled(provenance)
        case FixtureCase.SUCCESS:
            raise FixtureOutcomeError(fixture_case)


@dataclass(frozen=True, slots=True)
class FixtureOutcomeError(Exception):
    fixture_case: FixtureCase

    @override
    def __str__(self) -> str:
        return f'{self.fixture_case.value} requires provider-specific fixture data'


@dataclass(frozen=True, slots=True)
class ProductionTransportDisabledError(Exception):
    environment_name: str

    @override
    def __str__(self) -> str:
        return f'production transport requires {self.environment_name}=1'


class PublicHttpClient(Protocol):
    def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response: ...

    def close(self) -> None: ...


@dataclass(frozen=True, slots=True)
class LiveTransport:
    client: PublicHttpClient

    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        try:
            response = self.client.get(url, headers=headers, timeout=30.0)
        except requests.RequestException:
            return MusicBrainzHttpResponse(status_code=503, body=b'')
        return MusicBrainzHttpResponse(status_code=response.status_code, body=response.content)


def build_live_transport(
    client_factory: Callable[[], PublicHttpClient] = requests.Session,
) -> LiveTransport:
    if os.environ.get(LIVE_TRANSPORT_ENVIRONMENT) != '1':
        raise ProductionTransportDisabledError(LIVE_TRANSPORT_ENVIRONMENT)
    return LiveTransport(client=client_factory())
