from __future__ import annotations

import http.client
import logging
import re
import socket
import ssl
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from enum import StrEnum
from pathlib import Path
from threading import Lock
from typing import Final, Protocol, final, override
from urllib.parse import urlsplit

import requests
from requests.adapters import HTTPAdapter
from requests.structures import CaseInsensitiveDict
from sqlalchemy.orm import Session
from urllib3.util import Retry

from music_ingest.models import RuntimeSettingRecord
from music_ingest.repositories.persistence import ProviderPersistenceRepository

SHA256_HEX_PATTERN: Final = re.compile(r'^[0-9a-f]{64}$')
HTTP_STATUS_MINIMUM: Final = 100
HTTP_STATUS_MAXIMUM: Final = 599
LOGGER = logging.getLogger(__name__)


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
_DEFAULT_REQUEST_INTERVALS: Final = {
    ProviderName.MUSICBRAINZ: timedelta(seconds=1.5),
    ProviderName.ACOUSTID: timedelta(seconds=1 / 3),
}
_ACOUSTID_HOST: Final = 'api.acoustid.org'
_LEASE_DURATION = timedelta(minutes=1)


class SessionFactory(Protocol):
    def __call__(self) -> Session: ...


class RequestRateLimiter(Protocol):
    def wait(self, provider_name: str) -> None: ...


@final
class DatabaseRequestRateLimiter:
    def __init__(
        self,
        session_factory: SessionFactory,
        sleep: Callable[[float], None] = time.sleep,
        request_interval: timedelta | None = None,
        disabled_providers: frozenset[str] = frozenset(),
    ) -> None:
        self._session_factory: SessionFactory = session_factory
        self._sleep: Callable[[float], None] = sleep
        self._request_interval = request_interval
        self._disabled_providers = set(disabled_providers)
        self._disabled_providers_lock = Lock()

    def set_provider_disabled(self, provider_name: str, disabled: bool) -> None:
        with self._disabled_providers_lock:
            if disabled:
                self._disabled_providers.add(provider_name)
            else:
                self._disabled_providers.discard(provider_name)

    def wait(self, provider_name: str) -> None:
        with self._disabled_providers_lock:
            if provider_name in self._disabled_providers:
                return
        if self._request_interval is not None and self._request_interval <= timedelta():
            return
        now = datetime.now(UTC)
        with self._session_factory() as session:
            configured_interval = self._request_interval
            if configured_interval is None:
                stored_interval = session.get(RuntimeSettingRecord, f'providers.{provider_name}.request_delay_seconds')
                if stored_interval is None:
                    configured_interval = _DEFAULT_REQUEST_INTERVALS[ProviderName(provider_name)]
                else:
                    configured_interval = timedelta(seconds=float(stored_interval.value))
            if configured_interval <= timedelta():
                return
            reservation = ProviderPersistenceRepository(session).reserve_next_start(
                provider_name, now, configured_interval, _LEASE_DURATION
            )
            session.commit()
        delay = (reservation.scheduled_start - datetime.now(UTC)).total_seconds()
        if delay > 0:
            self._sleep(delay)


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
    status_code: int | None
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
    recording_mbid: str | None = None
    release_title: str | None = None
    artist_name: str | None = None
    release_mbid: str | None = None
    recording_title: str | None = None
    duration_seconds: int | None = None
    track_number: int | None = None
    recording_mbids: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class AcoustIdLookupRequest:
    fingerprint: str
    fixture_case: FixtureCase
    duration_seconds: float = 0.0


@dataclass(frozen=True, slots=True)
class ReleaseCandidate:
    release_mbid: str
    release_title: str
    artist_name: str
    duration_seconds: int | None = None
    recording_mbids: tuple[str, ...] = ()
    track_mbids: tuple[str, ...] = ()
    recording_title: str | None = None
    date: str | None = None
    original_date: str | None = None
    country: str | None = None
    track_number: int | None = None
    track_total: int | None = None
    disc_number: int | None = None
    disc_total: int | None = None
    genres: tuple[str, ...] = ()
    release_group_mbid: str | None = None
    isrcs: tuple[str, ...] = ()
    performers: tuple[str, ...] = ()
    release_artist_name: str | None = None
    recording_artist_names: tuple[str, ...] = ()
    release_artist_names: tuple[str, ...] = ()
    recording_artist_mbids: tuple[str, ...] = ()
    release_artist_mbids: tuple[str, ...] = ()
    catalog_numbers: tuple[str, ...] = ()
    disambiguation: str | None = None
    musicbrainz_score: float | None = None
    recording_candidates: tuple[ReleaseCandidate, ...] = ()


def release_display_title(candidate: ReleaseCandidate) -> str:
    disambiguation = (candidate.disambiguation or '').strip()
    return f'{candidate.release_title} ({disambiguation})' if disambiguation else candidate.release_title


@dataclass(frozen=True, slots=True)
class RecordingCandidate:
    recording_mbid: str
    score: float


@dataclass(frozen=True, slots=True)
class RecordingEvidence:
    recording_mbid: str
    score: float
    candidates: tuple[RecordingCandidate, ...] = ()


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
    candidates: tuple[ReleaseCandidate, ...] = ()


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


class MusicBrainzProvider(Protocol):
    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult: ...


class AcoustIdProvider(Protocol):
    def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdResult: ...


class PublicHttpClient(Protocol):
    def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response: ...

    def close(self) -> None: ...


class _IPv6HTTPSConnection(http.client.HTTPSConnection):
    sock: socket.socket | ssl.SSLSocket

    @override
    def connect(self) -> None:
        address = socket.getaddrinfo(self.host, self.port, socket.AF_INET6, socket.SOCK_STREAM)[0][4]
        sock = socket.socket(socket.AF_INET6, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        sock.connect(address)
        self.sock = ssl.create_default_context().wrap_socket(sock, server_hostname=self.host)


@dataclass(frozen=True, slots=True)
class _IPv6FirstClient:
    session: requests.Session

    def get(self, url: str, *, headers: dict[str, str], timeout: float) -> requests.Response:
        if urlsplit(url).hostname == 'musicbrainz.org':
            try:
                return self._get_ipv6(url, headers=headers, timeout=timeout)
            except OSError:
                pass
        return self.session.get(url, headers=headers, timeout=timeout)

    def close(self) -> None:
        self.session.close()

    @staticmethod
    def _get_ipv6(url: str, *, headers: dict[str, str], timeout: float) -> requests.Response:
        parsed = urlsplit(url)
        connection = _IPv6HTTPSConnection(parsed.hostname or '', parsed.port or 443, timeout=timeout)
        try:
            path = parsed.path or '/'
            if parsed.query:
                path = f'{path}?{parsed.query}'
            connection.request('GET', path, headers=headers)
            response = connection.getresponse()
            result = requests.Response()
            result.status_code = response.status
            result.headers = CaseInsensitiveDict(dict(response.getheaders()))
            object.__setattr__(result, '_content', response.read())
            result.url = url
            return result
        finally:
            connection.close()


@dataclass(frozen=True, slots=True)
class LiveTransport:
    client: PublicHttpClient
    limiter: RequestRateLimiter | None = None
    sleep: Callable[[float], None] = time.sleep
    musicbrainz_host: Callable[[], str] = lambda: 'https://musicbrainz.org'

    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        for attempt in range(3):
            provider_name = _provider_name_for_url(url, self.musicbrainz_host())
            host = urlsplit(url).hostname or ''
            waiting_started = time.monotonic()
            if self.limiter is not None and provider_name is not None:
                LOGGER.info('provider rate-limit wait provider=%s host=%s attempt=%s', provider_name, host, attempt + 1)
                self.limiter.wait(provider_name)
            LOGGER.info(
                'provider request start provider=%s host=%s attempt=%s wait_seconds=%.3f',
                provider_name,
                host,
                attempt + 1,
                time.monotonic() - waiting_started,
            )
            request_started = time.monotonic()
            try:
                response = self.client.get(url, headers=headers, timeout=10.0)
            except requests.RequestException:
                LOGGER.warning(
                    'provider request failed provider=%s host=%s attempt=%s request_seconds=%.3f',
                    provider_name,
                    host,
                    attempt + 1,
                    time.monotonic() - request_started,
                )
                return MusicBrainzHttpResponse(status_code=None, body=b'')
            LOGGER.info(
                'provider request complete provider=%s host=%s attempt=%s status=%s request_seconds=%.3f',
                provider_name,
                host,
                attempt + 1,
                response.status_code,
                time.monotonic() - request_started,
            )
            if response.status_code not in {429, 500, 502, 503, 504} or attempt == 2:
                return MusicBrainzHttpResponse(status_code=response.status_code, body=response.content)
            retry_after = response.headers.get('Retry-After')
            if retry_after is not None:
                with suppress(ValueError):
                    self.sleep(max(float(retry_after), 0.0))
        raise AssertionError('unreachable transport retry state')


def _provider_name_for_url(url: str, musicbrainz_host: str) -> ProviderName | None:
    hostname = urlsplit(url).hostname
    if hostname == urlsplit(musicbrainz_host).hostname:
        return ProviderName.MUSICBRAINZ
    if hostname == _ACOUSTID_HOST:
        return ProviderName.ACOUSTID
    return None


def _default_live_client() -> PublicHttpClient:
    client = requests.Session()
    retries = Retry(total=0)
    client.mount('https://', HTTPAdapter(max_retries=retries))
    return _IPv6FirstClient(client)


def build_live_transport(
    client_factory: Callable[[], PublicHttpClient] = _default_live_client,
    limiter: RequestRateLimiter | None = None,
    musicbrainz_host: Callable[[], str] = lambda: 'https://musicbrainz.org',
) -> LiveTransport:
    return LiveTransport(client=client_factory(), limiter=limiter, musicbrainz_host=musicbrainz_host)
