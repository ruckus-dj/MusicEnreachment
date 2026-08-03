from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from typing import ClassVar

from pydantic import BaseModel, ConfigDict, ValidationError
from sqlalchemy.orm import Session

from music_ingest.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    AcoustIdProvider,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    FixtureCase,
    FixtureProvenance,
    LiveProvenance,
    Malformed,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzProvider,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    RecordingEvidence,
    ReleaseCandidate,
    Timeout,
    Unavailable,
)
from music_ingest.persistence.models import ProviderSnapshotRecord
from music_ingest.persistence.repository import ProviderPersistenceRepository

_FRESHNESS = timedelta(hours=24)
_INTERVAL = timedelta(seconds=1)
_LEASE_DURATION = timedelta(minutes=1)


class _FixturePayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    outcome: FixtureCase
    release_mbid: str | None = None
    release_title: str | None = None
    artist_name: str | None = None
    recording_mbid: str | None = None
    score: float | None = None


@dataclass(frozen=True, slots=True)
class ProviderEvidenceRequest:
    query: str
    musicbrainz_case: FixtureCase
    fingerprint: str | None
    acoustid_case: FixtureCase | None


@dataclass(frozen=True, slots=True)
class ProviderEvidenceResult:
    musicbrainz: MusicBrainzResult
    acoustid: AcoustIdResult | None
    selected_release: None = None


@dataclass(frozen=True, slots=True)
class ProviderEvidenceService:
    session: Session
    musicbrainz: MusicBrainzProvider
    acoustid: AcoustIdProvider | None
    wait_until: Callable[[datetime], None] | None = None

    def lookup(self, request: ProviderEvidenceRequest, now: datetime) -> ProviderEvidenceResult:
        musicbrainz = self._lookup_musicbrainz(request, now)
        acoustid = self._lookup_acoustid(request, now)
        return ProviderEvidenceResult(musicbrainz=musicbrainz, acoustid=acoustid)

    def _lookup_musicbrainz(self, request: ProviderEvidenceRequest, now: datetime) -> MusicBrainzResult:
        request_hash = sha256(request.query.encode()).hexdigest()
        cached = self._fresh_snapshot('musicbrainz', request_hash, now)
        if cached is not None:
            return _decode_musicbrainz(cached, 'cached')
        self._reserve_start('musicbrainz', now)
        result = self.musicbrainz.lookup(MusicBrainzLookupRequest(request.query, request.musicbrainz_case), now)
        return self._persist_musicbrainz(result, request_hash, now)

    def _lookup_acoustid(self, request: ProviderEvidenceRequest, now: datetime) -> AcoustIdResult | None:
        match self.acoustid, request.fingerprint, request.acoustid_case:
            case _, None, _:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case _, _, None:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case None, _, _:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case provider, str() as fingerprint, FixtureCase() as fixture_case:
                request_hash = sha256(fingerprint.encode()).hexdigest()
                cached = self._fresh_snapshot('acoustid', request_hash, now)
                if cached is not None:
                    return _decode_acoustid(cached, 'cached')
                self._reserve_start('acoustid', now)
                result = provider.lookup(AcoustIdLookupRequest(fingerprint, fixture_case), now)
                return self._persist_acoustid(result, request_hash, now)

    def _fresh_snapshot(self, provider_name: str, request_hash: str, now: datetime) -> ProviderSnapshotRecord | None:
        snapshot = ProviderPersistenceRepository(self.session).newest_relevant(provider_name, request_hash)
        if snapshot is None:
            return None
        captured_at = _utc(snapshot.captured_at)
        age = now - captured_at
        if age < _FRESHNESS and snapshot.response_body is not None:
            return snapshot
        _ = ProviderPersistenceRepository(self.session).append_snapshot(
            provider_name=provider_name,
            request_hash=request_hash,
            request_descriptor='cached provider response expired',
            response_sha256=snapshot.response_sha256,
            response_body=None,
            captured_at=now,
            outcome=snapshot.outcome,
            state='stale',
            age_seconds=int(age.total_seconds()),
            http_status=snapshot.http_status,
        )
        self.session.commit()
        return None

    def _reserve_start(self, provider_name: str, now: datetime) -> None:
        reservation = ProviderPersistenceRepository(self.session).reserve_next_start(
            provider_name, now, _INTERVAL, _LEASE_DURATION
        )
        self.session.commit()
        (self.wait_until or _wait_until)(reservation.scheduled_start)

    def _persist_musicbrainz(self, result: MusicBrainzResult, request_hash: str, now: datetime) -> MusicBrainzResult:
        raw = _raw_response(result)
        persisted = _provenance('musicbrainz', request_hash, raw, _status(result), now, 'fresh')
        _ = ProviderPersistenceRepository(self.session).append_snapshot(
            provider_name='musicbrainz',
            request_hash=request_hash,
            request_descriptor='musicbrainz v2 lookup',
            response_sha256=persisted.sha256,
            response_body=raw,
            captured_at=now,
            outcome=_outcome(result),
            state='fresh',
            http_status=persisted.http_status,
        )
        self.session.commit()
        return _with_musicbrainz_provenance(result, persisted)

    def _persist_acoustid(self, result: AcoustIdResult, request_hash: str, now: datetime) -> AcoustIdResult:
        raw = _raw_response(result)
        persisted = _provenance('acoustid', request_hash, raw, _status(result), now, 'fresh')
        _ = ProviderPersistenceRepository(self.session).append_snapshot(
            provider_name='acoustid',
            request_hash=request_hash,
            request_descriptor='acoustid recording evidence lookup',
            response_sha256=persisted.sha256,
            response_body=raw,
            captured_at=now,
            outcome=_outcome(result),
            state='fresh',
            http_status=persisted.http_status,
        )
        self.session.commit()
        return _with_acoustid_provenance(result, persisted)


def _wait_until(scheduled_start: datetime) -> None:
    delay = (scheduled_start - datetime.now(UTC)).total_seconds()
    if delay > 0:
        time.sleep(delay)


def _disabled_request_hash() -> str:
    return sha256(b'acoustid-disabled').hexdigest()


def _utc(value: datetime) -> datetime:
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value


def _provenance(
    provider_name: str, request_hash: str, raw: bytes, http_status: int | None, captured_at: datetime, state: str
) -> LiveProvenance:
    return LiveProvenance(provider_name, request_hash, sha256(raw).hexdigest(), http_status, captured_at, state)


def _raw_response(result: MusicBrainzResult | AcoustIdResult) -> bytes:
    match result.provenance:
        case FixtureProvenance(path=path):
            return path.read_bytes()
        case LiveProvenance():
            return b''


def _status(result: MusicBrainzResult | AcoustIdResult) -> int | None:
    match result.provenance:
        case LiveProvenance(http_status=status):
            return status
        case FixtureProvenance():
            return None


def _outcome(result: MusicBrainzResult | AcoustIdResult) -> str:
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


def _with_musicbrainz_provenance(result: MusicBrainzResult, provenance: LiveProvenance) -> MusicBrainzResult:
    match result:
        case MusicBrainzMatch(candidate=candidate):
            return MusicBrainzMatch(provenance, candidate)
        case NoMatch():
            return NoMatch(provenance)
        case Ambiguous():
            return Ambiguous(provenance)
        case Timeout():
            return Timeout(provenance)
        case Malformed():
            return Malformed(provenance)
        case RateLimited():
            return RateLimited(provenance)
        case Unavailable():
            return Unavailable(provenance)
        case Disabled():
            return Disabled(provenance)


def _with_acoustid_provenance(result: AcoustIdResult, provenance: LiveProvenance) -> AcoustIdResult:
    match result:
        case AcoustIdMatch(evidence=evidence):
            return AcoustIdMatch(provenance, evidence)
        case NoMatch():
            return NoMatch(provenance)
        case Ambiguous():
            return Ambiguous(provenance)
        case Timeout():
            return Timeout(provenance)
        case Malformed():
            return Malformed(provenance)
        case RateLimited():
            return RateLimited(provenance)
        case Unavailable():
            return Unavailable(provenance)
        case Disabled():
            return Disabled(provenance)


def _decode_musicbrainz(snapshot: ProviderSnapshotRecord, state: str) -> MusicBrainzResult:
    provenance = _provenance(
        'musicbrainz',
        snapshot.request_hash,
        snapshot.response_body or b'',
        snapshot.http_status,
        _utc(snapshot.captured_at),
        state,
    )
    payload = _payload(snapshot.response_body)
    match payload:
        case _FixturePayload(
            outcome=FixtureCase.SUCCESS,
            release_mbid=str() as mbid,
            release_title=str() as title,
            artist_name=str() as artist,
        ):
            return MusicBrainzMatch(provenance, ReleaseCandidate(mbid, title, artist))
        case _FixturePayload(outcome=outcome):
            return _failed(outcome, provenance)
        case None:
            return Malformed(provenance)


def _decode_acoustid(snapshot: ProviderSnapshotRecord, state: str) -> AcoustIdResult:
    provenance = _provenance(
        'acoustid',
        snapshot.request_hash,
        snapshot.response_body or b'',
        snapshot.http_status,
        _utc(snapshot.captured_at),
        state,
    )
    payload = _payload(snapshot.response_body)
    match payload:
        case _FixturePayload(outcome=FixtureCase.SUCCESS, recording_mbid=str() as mbid, score=float() as score):
            return AcoustIdMatch(provenance, RecordingEvidence(mbid, score))
        case _FixturePayload(outcome=outcome):
            return _failed(outcome, provenance)
        case None:
            return Malformed(provenance)


def _payload(raw: bytes | None) -> _FixturePayload | None:
    try:
        return _FixturePayload.model_validate_json(raw or b'')
    except ValidationError:
        return None


def _failed(
    case: FixtureCase, provenance: LiveProvenance
) -> NoMatch | Ambiguous | Timeout | Malformed | RateLimited | Unavailable | Disabled:
    match case:
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
            return Malformed(provenance)
