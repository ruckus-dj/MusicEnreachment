from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from hashlib import sha256
from typing import ClassVar
from urllib.parse import urlencode

from pydantic import BaseModel, ConfigDict, ValidationError

from music_ingest.matching.musicbrainz import MusicBrainzTransport
from music_ingest.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    AcoustIdResult,
    LiveProvenance,
    Malformed,
    NoMatch,
    RateLimited,
    RecordingEvidence,
    Unavailable,
)

_ENDPOINT = 'https://api.acoustid.org/v2/lookup'


class _Recording(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    id: str


class _Result(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    score: float
    recordings: tuple[_Recording, ...]


class _Response(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    status: str
    results: tuple[_Result, ...] = ()


@dataclass(frozen=True, slots=True)
class AcoustIdV2Adapter:
    transport: MusicBrainzTransport
    client_key: str

    def lookup(self, request: AcoustIdLookupRequest, now: datetime | None = None) -> AcoustIdResult:
        captured_at = now or datetime.now().astimezone()
        query = urlencode({'client': self.client_key, 'fingerprint': request.fingerprint, 'meta': 'recordings'})
        url = f'{_ENDPOINT}?{query}'
        response = self.transport.get(url, headers={'Accept': 'application/json'})
        provenance = LiveProvenance(
            'acoustid',
            sha256(request.fingerprint.encode()).hexdigest(),
            sha256(response.body).hexdigest(),
            response.status_code,
            captured_at,
            'fresh',
        )
        if response.status_code == 429:
            return RateLimited(provenance)
        if response.status_code >= 500:
            return Unavailable(provenance)
        try:
            payload = _Response.model_validate_json(response.body)
        except ValidationError:
            return Malformed(provenance)
        match payload.results:
            case (_Result(score=score, recordings=(_Recording(id=recording_mbid), *_)), *_):
                return AcoustIdMatch(provenance, RecordingEvidence(recording_mbid, score))
            case ():
                return NoMatch(provenance)
            case _:
                return Malformed(provenance)
