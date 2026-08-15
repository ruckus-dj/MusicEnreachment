from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from hashlib import sha256

from pydantic import ValidationError
from sqlalchemy.orm import Session

from music_ingest.dto import EvidenceFixturePayload as _FixturePayload
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
from music_ingest.models import ProviderSnapshotRecord
from music_ingest.models.repositories import ProviderPersistenceRepository

_FRESHNESS = timedelta(hours=24)


@dataclass(frozen=True, slots=True)
class ProviderEvidenceRequest:
    query: str
    musicbrainz_case: FixtureCase
    fingerprint: str | None
    acoustid_case: FixtureCase | None
    duration_seconds: float | None = None
    force_refresh: bool = False
    release_title: str | None = None
    acoustid_confidence_threshold: float = 0.7
    artist_name: str | None = None
    recording_mbid: str | None = None
    release_mbid: str | None = None
    run_acoustid: bool = True
    run_musicbrainz: bool = True


@dataclass(frozen=True, slots=True)
class ProviderEvidenceResult:
    musicbrainz: MusicBrainzResult
    acoustid: AcoustIdResult | None
    selected_release: None = None


@dataclass(frozen=True, slots=True)
class ProviderEvidenceService:
    session: Session
    musicbrainz: MusicBrainzProvider | None
    acoustid: AcoustIdProvider | None
    wait_until: Callable[[datetime], None] | None = None
    commit_on_persist: bool = True

    def lookup(self, request: ProviderEvidenceRequest, now: datetime) -> ProviderEvidenceResult:
        acoustid = self._lookup_acoustid(request, now)
        musicbrainz = self._lookup_musicbrainz(request, acoustid, now)
        return ProviderEvidenceResult(musicbrainz=musicbrainz, acoustid=acoustid)

    def _lookup_musicbrainz(
        self, request: ProviderEvidenceRequest, acoustid: AcoustIdResult | None, now: datetime
    ) -> MusicBrainzResult:
        if (
            not request.run_musicbrainz
            or self.musicbrainz is None
            or (not request.query and request.recording_mbid is None and request.release_mbid is None)
        ):
            return Disabled(_provenance('musicbrainz', _disabled_request_hash(), b'', None, now, 'fresh'))

        match request.release_mbid, request.recording_mbid, acoustid:
            case str() as release_mbid, str() as recording_mbid, _:
                musicbrainz_request = MusicBrainzLookupRequest(
                    request.query,
                    request.musicbrainz_case,
                    recording_mbid,
                    request.release_title,
                    request.artist_name,
                    release_mbid,
                )
                request_hash = sha256(f'release:{release_mbid}'.encode()).hexdigest()
            case None, str() as recording_mbid, _:
                musicbrainz_request = MusicBrainzLookupRequest(
                    request.query,
                    request.musicbrainz_case,
                    recording_mbid,
                    request.release_title,
                    request.artist_name,
                )
                request_hash = sha256(f'recording:{recording_mbid}'.encode()).hexdigest()
            case None, None, AcoustIdMatch(evidence=evidence) if (
                evidence.score >= request.acoustid_confidence_threshold
            ):
                musicbrainz_request = MusicBrainzLookupRequest(
                    request.query,
                    request.musicbrainz_case,
                    evidence.recording_mbid,
                    request.release_title,
                    request.artist_name,
                )
                request_hash = sha256(f'recording:{evidence.recording_mbid}'.encode()).hexdigest()
            case _:
                musicbrainz_request = MusicBrainzLookupRequest(request.query, request.musicbrainz_case)
                request_hash = sha256(request.query.encode()).hexdigest()
        cached = None if request.force_refresh else self._fresh_snapshot('musicbrainz', request_hash, now)
        if cached is not None:
            return _decode_musicbrainz(cached, 'cached')
        if self.wait_until is not None:
            self.wait_until(now)
        result = self.musicbrainz.lookup(musicbrainz_request, now)
        return self._persist_musicbrainz(result, request_hash, now)

    def _lookup_acoustid(self, request: ProviderEvidenceRequest, now: datetime) -> AcoustIdResult | None:
        duration_seconds = request.duration_seconds
        if not request.run_acoustid:
            return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
        if duration_seconds is None:
            return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
        match self.acoustid, request.fingerprint, request.acoustid_case:
            case _, None, _:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case _, _, None:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case None, _, _:
                return Disabled(_provenance('acoustid', _disabled_request_hash(), b'', None, now, 'fresh'))
            case provider, str() as fingerprint, FixtureCase() as fixture_case:
                request_hash = sha256(fingerprint.encode()).hexdigest()
                cached = None if request.force_refresh else self._fresh_snapshot('acoustid', request_hash, now)
                if cached is not None:
                    return _decode_acoustid(cached, 'cached')
                if self.wait_until is not None:
                    self.wait_until(now)
                result = provider.lookup(AcoustIdLookupRequest(fingerprint, fixture_case, duration_seconds), now)
                return self._persist_acoustid(result, request_hash, now)

    def _fresh_snapshot(self, provider_name: str, request_hash: str, now: datetime) -> ProviderSnapshotRecord | None:
        snapshot = ProviderPersistenceRepository(self.session).newest_relevant(provider_name, request_hash)
        if snapshot is None:
            return None
        captured_at = _utc(snapshot.captured_at)
        age = now - captured_at
        if age < _FRESHNESS and snapshot.response_body:
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
        self._persist()
        return None

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
        self._persist()
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
        self._persist()
        return _with_acoustid_provenance(result, persisted)

    def _persist(self) -> None:
        if self.commit_on_persist:
            self.session.commit()
            return
        self.session.flush()


def _disabled_request_hash() -> str:
    return sha256(b'acoustid-disabled').hexdigest()


def _utc(value: datetime) -> datetime:
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value


def _provenance(
    provider_name: str, request_hash: str, raw: bytes, http_status: int | None, captured_at: datetime, state: str
) -> LiveProvenance:
    return LiveProvenance(provider_name, request_hash, sha256(raw).hexdigest(), http_status, captured_at, state, raw)


def _raw_response(result: MusicBrainzResult | AcoustIdResult) -> bytes:
    match result.provenance:
        case FixtureProvenance(path=path):
            return path.read_bytes()
        case LiveProvenance(response_body=body):
            return body


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
        case Ambiguous(candidates=candidates):
            return Ambiguous(provenance, candidates)
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
            return _failed(FixtureCase(outcome), provenance)
        case None:
            return Malformed(provenance)
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
            return _failed(FixtureCase(outcome), provenance)
        case None:
            return Malformed(provenance)
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
