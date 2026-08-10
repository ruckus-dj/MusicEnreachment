from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from hashlib import sha256
from pathlib import Path
from typing import ClassVar, override

from pydantic import BaseModel, ConfigDict, ValidationError

from music_ingest.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    FixtureCase,
    FixtureProvenance,
    Malformed,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    Provenance,
    RateLimited,
    RecordingEvidence,
    ReleaseCandidate,
    Timeout,
    Unavailable,
)


class FixturePayloadDto(BaseModel):
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


type FixtureOutcome = NoMatch | Ambiguous | Timeout | Malformed | RateLimited | Unavailable | Disabled


@dataclass(frozen=True, slots=True)
class MusicBrainzFixtureProvider:
    fixture_directory: Path

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        _ = now
        payload, provenance = _fixture_payload(self.fixture_directory, request.fixture_case)
        match payload:
            case None:
                return Malformed(provenance)
            case FixturePayloadDto(outcome=FixtureCase.SUCCESS):
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
            case FixturePayloadDto(outcome=outcome):
                return _fixture_outcome(FixtureCase(outcome), provenance)
        return Malformed(provenance)


@dataclass(frozen=True, slots=True)
class AcoustIdFixtureProvider:
    fixture_directory: Path

    def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdResult:
        _ = now
        payload, provenance = _fixture_payload(self.fixture_directory, request.fixture_case)
        match payload:
            case None:
                return Malformed(provenance)
            case FixturePayloadDto(outcome=FixtureCase.SUCCESS):
                match payload.recording_mbid, payload.score:
                    case str() as recording_mbid, float() as score:
                        return AcoustIdMatch(provenance, RecordingEvidence(recording_mbid, score))
                    case _:
                        return Malformed(provenance)
            case FixturePayloadDto(outcome=outcome):
                return _fixture_outcome(FixtureCase(outcome), provenance)
        return Malformed(provenance)


def _fixture_payload(
    fixture_directory: Path, fixture_case: FixtureCase
) -> tuple[FixturePayloadDto | None, FixtureProvenance]:
    fixture_path = fixture_directory / f'{fixture_case.value}.json'
    raw_fixture = fixture_path.read_bytes()
    provenance = FixtureProvenance(path=fixture_path, sha256=sha256(raw_fixture).hexdigest())
    try:
        return FixturePayloadDto.model_validate_json(raw_fixture), provenance
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
