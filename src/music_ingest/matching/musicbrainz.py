from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from typing import ClassVar, Protocol
from urllib.parse import urlencode

from pydantic import BaseModel, ConfigDict, ValidationError

from music_ingest.matching.providers import (
    Ambiguous,
    LiveProvenance,
    Malformed,
    MusicBrainzHttpResponse,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    ReleaseCandidate,
    Unavailable,
)

_ENDPOINT = 'https://musicbrainz.org/ws/2/release/'


class MusicBrainzTransport(Protocol):
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


class _ArtistCredit(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    name: str


class _Release(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    id: str
    title: str
    artist_credit: tuple[_ArtistCredit, ...]


class _Response(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[_Release, ...]


@dataclass(frozen=True, slots=True)
class MusicBrainzV2Adapter:
    transport: MusicBrainzTransport
    user_agent: str

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        captured_at = now or datetime.now(UTC)
        url = f'{_ENDPOINT}?{urlencode({"query": request.query, "fmt": "json"})}'
        response = self.transport.get(url, headers={'User-Agent': self.user_agent, 'Accept': 'application/json'})
        provenance = LiveProvenance(
            'musicbrainz',
            sha256(request.query.encode()).hexdigest(),
            sha256(response.body).hexdigest(),
            response.status_code,
            captured_at,
            'fresh',
            response.body,
        )
        if response.status_code == 429:
            return RateLimited(provenance)
        if response.status_code >= 500:
            return Unavailable(provenance)
        if response.status_code != 200:
            return Malformed(provenance)
        try:
            payload = _Response.model_validate_json(response.body)
        except ValidationError:
            return Malformed(provenance)
        match payload.releases:
            case ():
                return NoMatch(provenance)
            case (release,):
                artist = ''.join(item.name for item in release.artist_credit)
                return MusicBrainzMatch(provenance, ReleaseCandidate(release.id, release.title, artist))
            case _:
                return Ambiguous(provenance)
