from __future__ import annotations

import math
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime
from hashlib import sha256
from threading import Lock
from typing import Final, Protocol, final
from urllib.parse import urlencode

import requests
from pydantic import TypeAdapter, ValidationError

from music_ingest.dto.lrclib import RecordPayload
from music_ingest.matching.scoring import text_similarity

_PROVIDER_NAME: Final = 'lrclib'
_LRCLIB_HOST: Final = 'https://lrclib.net'
_SEARCH_PATH: Final = '/api/search'
_RETRY_STATUSES: Final = frozenset({429, 500, 502, 503, 504})
_RETRYABLE_EXCEPTIONS: Final = (requests.RequestException,)
_RETRY_DELAY_SECONDS: Final = 1.0
# Never hand a client a non-positive timeout: the budget clamp below must still be a legal request timeout.
_MIN_TIMEOUT_SECONDS: Final = 0.001
_DURATION_TOLERANCE_SECONDS: Final = 2.0
_NO_RESPONSE_REASON: Final = 'lrclib request failed before a response was received'
_NO_LYRICS_REASON: Final = 'lrclib has no lyrics for this track'
_RATE_LIMITED_REASON: Final = 'lrclib rate limited the request'
_OVERSIZED_REASON: Final = 'lrclib response body exceeded the managed size bound'
_MALFORMED_REASON: Final = 'lrclib response was malformed'
_NO_RECORD_IDENTITY_REASON: Final = 'lrclib response did not carry a usable record identity'
_NO_SYNCED_LYRICS_REASON: Final = 'lrclib search returned no synced candidate matching the published duration'
_DURATION_REASON: Final = 'lrclib candidate duration is missing or outside the accepted tolerance'
_CONFIDENCE_REASON: Final = 'best lrclib synced candidate is below the managed match confidence threshold'
DEFAULT_USER_AGENT: Final = 'music-ingest/0.1.0 (music-ingest@example.com)'
# Provider payloads are untrusted: never buffer more than this much of one response body.
MAX_RESPONSE_BODY_BYTES: Final = 4 * 1024 * 1024
_READ_CHUNK_BYTES: Final = 64 * 1024


@dataclass(frozen=True, slots=True)
class LrclibHttpResponse:
    """One bounded provider response; ``truncated`` marks a body stopped at the managed size bound."""

    status_code: int | None
    body: bytes
    truncated: bool = False


class LrclibTransport(Protocol):
    """Minimal HTTP seam so provider lookups stay offline-testable."""

    def get(self, url: str, *, headers: dict[str, str]) -> LrclibHttpResponse: ...


@dataclass(frozen=True, slots=True)
class LrclibLookupRequest:
    """Canonical identity of the published audio used for one lookup.

    ``album_name`` is optional: LRCLIB is asked about the album only when the published release names one.
    """

    track_name: str
    artist_name: str
    duration_seconds: int
    album_name: str | None = None


@dataclass(frozen=True, slots=True)
class LrclibProvenance:
    """Evidence for one lookup; it never carries lyric text or the raw provider body.

    ``request_hash`` digests the exact lookup URL, ``response_sha256`` digests the bounded body accepted from
    the provider (a truncated body digests only the retained prefix), and ``provider_record_id`` is the LRCLIB
    record identity that every accepted result must carry.
    """

    request_hash: str
    response_sha256: str
    provider_name: str
    endpoint: str
    http_status: int | None
    captured_at: datetime
    provider_record_id: int | None = None


@dataclass(frozen=True, slots=True)
class LrclibSynced:
    """Accepted provider lyrics plus the evidence that identified them; lyric text stays out of ``repr``."""

    provenance: LrclibProvenance
    synced_lyrics: str = field(repr=False)


@dataclass(frozen=True, slots=True)
class LrclibNoCandidate:
    provenance: LrclibProvenance
    reason: str


@dataclass(frozen=True, slots=True)
class LrclibProviderError:
    provenance: LrclibProvenance
    reason: str


type LrclibResult = LrclibSynced | LrclibNoCandidate | LrclibProviderError


@dataclass(frozen=True, slots=True)
class LrclibSettings:
    """One immutable snapshot of the operational lrclib values persisted by the settings layer."""

    enabled: bool = True
    host: str = _LRCLIB_HOST
    user_agent: str = DEFAULT_USER_AGENT
    timeout_seconds: float = 15.0
    max_attempts: int = 3
    request_delay_seconds: float = 0.3
    max_response_bytes: int = MAX_RESPONSE_BODY_BYTES
    match_confidence_threshold: float = 0.7

    @property
    def endpoint(self) -> str:
        """The search endpoint of the configured host; provenance never hardcodes the public host."""
        return f'{self.host}{_SEARCH_PATH}'

    @property
    def retry_budget_seconds(self) -> float:
        """Hard ceiling on one lookup: every attempt may spend at most one configured request timeout."""
        return self.timeout_seconds * self.max_attempts


@final
class LrclibSettingsHolder:
    """Thread-safe, replaceable view of the persisted lrclib settings shared by the adapter and its transport.

    Every worker slot reads the same holder while the settings API replaces the whole snapshot on save, so readers
    take one atomic snapshot instead of observing a half-applied update.
    """

    def __init__(self, settings: LrclibSettings | None = None) -> None:
        self._lock: Lock = Lock()
        self._settings: LrclibSettings = LrclibSettings() if settings is None else settings

    def snapshot(self) -> LrclibSettings:
        """Return the settings the next request must use; a saved change applies without a restart."""
        with self._lock:
            return self._settings

    def update(self, settings: LrclibSettings) -> None:
        """Replace the settings the adapter and its transport read from now on."""
        with self._lock:
            self._settings = settings


def lookup_url(request: LrclibLookupRequest, host: str = _LRCLIB_HOST) -> str:
    """Build the structured ``/api/search`` request; duration is a local hard acceptance gate."""
    parameters: dict[str, str] = {
        'artist_name': request.artist_name,
        'track_name': request.track_name,
    }
    album_name = (request.album_name or '').strip()
    if album_name:
        parameters['album_name'] = album_name
    return f'{host}{_SEARCH_PATH}?{urlencode(parameters)}'


@dataclass(frozen=True, slots=True)
class LrclibAdapter:
    """Find and deterministically rank synced-lyrics candidates from LRCLIB search.

    Every operational value comes from the live settings snapshot of the shared holder, so an operator's saved
    change reaches the next lookup without restarting the worker.
    """

    transport: LrclibTransport
    settings: LrclibSettingsHolder = field(default_factory=LrclibSettingsHolder)

    @property
    def enabled(self) -> bool:
        """Whether the persisted provider switch still allows any provider traffic."""
        return self.settings.snapshot().enabled

    def lookup(self, request: LrclibLookupRequest, now: datetime | None = None) -> LrclibResult:
        settings = self.settings.snapshot()
        captured_at = now or datetime.now(UTC)
        url = lookup_url(request, settings.host)
        response = self.transport.get(url, headers=_request_headers(settings.user_agent))
        provenance = LrclibProvenance(
            request_hash=sha256(url.encode()).hexdigest(),
            response_sha256=sha256(response.body).hexdigest(),
            provider_name=_PROVIDER_NAME,
            endpoint=settings.endpoint,
            http_status=response.status_code,
            captured_at=captured_at,
        )
        if response.status_code is None:
            return LrclibProviderError(provenance, _NO_RESPONSE_REASON)
        if response.status_code == 404:
            return LrclibNoCandidate(provenance, _NO_LYRICS_REASON)
        if response.status_code == 429:
            return LrclibProviderError(provenance, _RATE_LIMITED_REASON)
        if response.status_code != 200:
            return LrclibProviderError(provenance, f'lrclib returned unexpected status {response.status_code}')
        if response.truncated or len(response.body) > settings.max_response_bytes:
            return LrclibProviderError(provenance, _OVERSIZED_REASON)
        try:
            payloads = TypeAdapter(list[RecordPayload]).validate_json(response.body)
        except ValidationError:
            return LrclibProviderError(provenance, _MALFORMED_REASON)
        candidates = tuple(
            payload
            for payload in payloads
            if payload.id is not None
            and payload.id > 0
            and payload.synced_lyrics is not None
            and payload.synced_lyrics.strip()
            and _duration_matches(request.duration_seconds, payload.duration)
        )
        if not candidates:
            return LrclibNoCandidate(provenance, _NO_SYNCED_LYRICS_REASON)
        best_score, best = min(
            ((_candidate_score(request, candidate), candidate) for candidate in candidates),
            key=lambda scored: (-scored[0], scored[1].id or 0),
        )
        if best_score < settings.match_confidence_threshold:
            return LrclibNoCandidate(provenance, _CONFIDENCE_REASON)
        if best.id is None or best.synced_lyrics is None:
            return LrclibProviderError(provenance, _NO_RECORD_IDENTITY_REASON)
        return LrclibSynced(replace(provenance, provider_record_id=best.id), best.synced_lyrics)


def _request_headers(user_agent: str) -> dict[str, str]:
    """Build the only headers lrclib.net is called with, from the configured user agent."""
    return {'User-Agent': user_agent, 'Accept': 'application/json'}


def _candidate_score(request: LrclibLookupRequest, payload: RecordPayload) -> float:
    """Average title, artist, and when available album similarity with stable equal-score selection by record id."""
    factors = [
        text_similarity(request.track_name, payload.track_name or ''),
        text_similarity(request.artist_name, payload.artist_name or ''),
    ]
    if (request.album_name or '').strip() and (payload.album_name or '').strip():
        factors.append(text_similarity(request.album_name or '', payload.album_name or ''))
    return sum(factors) / len(factors)


def _duration_matches(expected_seconds: int, provider_duration: float | None) -> bool:
    """A missing or non-finite provider duration rejects the candidate instead of lowering its confidence."""
    if provider_duration is None or not math.isfinite(provider_duration):
        return False
    return abs(provider_duration - expected_seconds) <= _DURATION_TOLERANCE_SECONDS


def _normalize(value: str) -> str:
    return ''.join(character for character in value.casefold() if character.isalnum())


class LrclibHttpClient(Protocol):
    """HTTP client seam for the live transport; ``requests.Session`` satisfies it."""

    def get(self, url: str, *, headers: dict[str, str], timeout: float, stream: bool) -> requests.Response: ...

    def close(self) -> None: ...


class _RequestPacer:
    """Reserve staggered request start times so concurrent callers cannot share one padding slot.

    The lock is held only long enough to reserve the next start time and the delay is slept outside it, so a caller
    that loses the race still waits for its own reserved slot instead of measuring a timestamp another thread is
    about to overwrite.
    """

    def __init__(
        self,
        clock: Callable[[], float] = time.monotonic,
        sleep: Callable[[float], None] = time.sleep,
    ) -> None:
        self._lock: Lock = Lock()
        self._clock: Callable[[], float] = clock
        self._sleep: Callable[[float], None] = sleep
        self._next_start_at: float | None = None

    def wait_for_turn(self, minimum_interval_seconds: float) -> None:
        """Reserve the next start time at least ``minimum_interval_seconds`` after the previous reservation."""
        interval = max(minimum_interval_seconds, 0.0)
        now = self._clock()
        with self._lock:
            start_at = now if self._next_start_at is None else max(now, self._next_start_at)
            self._next_start_at = start_at + interval
        delay = start_at - now
        if delay > 0.0:
            self._sleep(delay)


@final
class LiveLrclibTransport:
    """Concurrency-safe lrclib.net transport: paced request starts, bounded retries, bounded response bodies.

    One transport is shared by every worker slot of the runtime. Request starts are reserved under a lock and slept
    outside it, so concurrent lookups still stay ``request_delay_seconds`` apart, and one lookup is bounded by the
    configured per-request timeout/retry budget so an unreachable provider cannot pin a worker slot any longer.
    HTTP clients are per-thread: a ``requests.Session`` must never be driven from several threads at once.
    """

    def __init__(
        self,
        settings: LrclibSettingsHolder | None = None,
        *,
        client_factory: Callable[[], LrclibHttpClient] | None = None,
        clock: Callable[[], float] = time.monotonic,
        sleep: Callable[[float], None] = time.sleep,
    ) -> None:
        self._settings: LrclibSettingsHolder = LrclibSettingsHolder() if settings is None else settings
        self._client_factory: Callable[[], LrclibHttpClient] = (
            _default_live_client if client_factory is None else client_factory
        )
        self._clock: Callable[[], float] = clock
        self._sleep: Callable[[float], None] = sleep
        self._pacer: _RequestPacer = _RequestPacer(clock, sleep)
        self._local: threading.local = threading.local()
        self._clients: list[LrclibHttpClient] = []
        self._clients_lock: Lock = Lock()

    @property
    def settings(self) -> LrclibSettingsHolder:
        """The live settings holder this transport reads: the same one its adapter reads."""
        return self._settings

    def get(self, url: str, *, headers: dict[str, str]) -> LrclibHttpResponse:
        """Perform one bounded lookup: at most ``max_attempts`` paced attempts inside one retry budget."""
        settings = self._settings.snapshot()
        deadline = self._clock() + settings.retry_budget_seconds
        last = LrclibHttpResponse(status_code=None, body=b'')
        for attempt in range(settings.max_attempts):
            if not self._wait_for_turn(deadline, settings.request_delay_seconds):
                return last
            timeout = max(min(settings.timeout_seconds, deadline - self._clock()), _MIN_TIMEOUT_SECONDS)
            try:
                response = self._client().get(url, headers=headers, timeout=timeout, stream=True)
            except _RETRYABLE_EXCEPTIONS:
                last = LrclibHttpResponse(status_code=None, body=b'')
                retry_delay = _RETRY_DELAY_SECONDS
            else:
                if response.status_code not in _RETRY_STATUSES or attempt == settings.max_attempts - 1:
                    return _bounded_response(response, settings.max_response_bytes)
                retry_delay = _retry_after_seconds(response)
                last = LrclibHttpResponse(status_code=response.status_code, body=b'')
                response.close()
            if attempt == settings.max_attempts - 1 or not self._sleep_within_budget(retry_delay, deadline):
                return last
        return last

    def close(self) -> None:
        """Close every HTTP client this transport opened; the transport is unusable afterwards."""
        with self._clients_lock:
            clients, self._clients = self._clients, []
        for client in clients:
            client.close()

    def _client(self) -> LrclibHttpClient:
        client: LrclibHttpClient | None = getattr(self._local, 'client', None)
        if client is None:
            client = self._client_factory()
            self._local.client = client
            with self._clients_lock:
                self._clients = [*self._clients, client]
        return client

    def _wait_for_turn(self, deadline: float, request_delay_seconds: float) -> bool:
        """Reserve this attempt's paced slot; ``False`` means the retry budget is already spent."""
        if self._clock() >= deadline:
            return False
        self._pacer.wait_for_turn(request_delay_seconds)
        return self._clock() < deadline

    def _sleep_within_budget(self, retry_delay_seconds: float, deadline: float) -> bool:
        """Sleep one retry delay without ever exceeding the remaining retry budget."""
        remaining = deadline - self._clock()
        if remaining <= 0.0:
            return False
        self._sleep(min(max(retry_delay_seconds, 0.0), remaining))
        return self._clock() < deadline


def _retry_after_seconds(response: requests.Response) -> float:
    """The provider's own retry hint, or the fixed backoff when it is absent or unusable."""
    retry_after = response.headers.get('Retry-After')
    if retry_after is None:
        return _RETRY_DELAY_SECONDS
    try:
        return max(float(retry_after), 0.0)
    except ValueError:
        return _RETRY_DELAY_SECONDS


def _bounded_response(response: requests.Response, max_response_bytes: int) -> LrclibHttpResponse:
    """Read one response body without ever retaining more than ``max_response_bytes`` of it."""
    try:
        declared_length = _declared_content_length(response)
        if declared_length is not None and declared_length > max_response_bytes:
            return LrclibHttpResponse(status_code=response.status_code, body=b'', truncated=True)
        body, truncated = _read_bounded_body(response, max_response_bytes)
    finally:
        response.close()
    return LrclibHttpResponse(status_code=response.status_code, body=body, truncated=truncated)


def _declared_content_length(response: requests.Response) -> int | None:
    declared = response.headers.get('Content-Length')
    if declared is None:
        return None
    try:
        return int(declared)
    except ValueError:
        return None


def _read_bounded_body(response: requests.Response, max_response_bytes: int) -> tuple[bytes, bool]:
    chunks: list[bytes] = []
    retained = 0
    for chunk in response.iter_content(chunk_size=_READ_CHUNK_BYTES):
        if not chunk:
            continue
        remaining = max_response_bytes - retained
        chunks.append(chunk[:remaining])
        retained += min(len(chunk), remaining)
        if len(chunk) > remaining:
            return b''.join(chunks), True
    return b''.join(chunks), False


def _default_live_client() -> LrclibHttpClient:
    client: LrclibHttpClient = requests.Session()
    return client


def build_lrclib_transport(
    settings: LrclibSettingsHolder | None = None,
    *,
    client_factory: Callable[[], LrclibHttpClient] | None = None,
) -> LiveLrclibTransport:
    """Build the shared live transport that reads every operational value from ``settings``."""
    return LiveLrclibTransport(settings, client_factory=client_factory)


@dataclass(frozen=True, slots=True)
class LrclibProvider:
    """One live lrclib provider: the shared adapter plus the holder that carries runtime settings into it."""

    adapter: LrclibAdapter
    settings: LrclibSettingsHolder

    def apply(self, settings: LrclibSettings) -> None:
        """Apply a freshly persisted settings snapshot to the live process, without a restart."""
        self.settings.update(settings)


def build_lrclib_provider(
    settings: LrclibSettings | None = None,
    *,
    client_factory: Callable[[], LrclibHttpClient] | None = None,
) -> LrclibProvider:
    """Wire the live provider: one settings holder, one concurrency-safe transport, one adapter reading both."""
    holder = LrclibSettingsHolder(settings)
    transport = build_lrclib_transport(holder, client_factory=client_factory)
    return LrclibProvider(adapter=LrclibAdapter(transport, holder), settings=holder)
