from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from typing import ClassVar, Protocol
from urllib.parse import quote, urlencode

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
_RECORDING_ENDPOINT = 'https://musicbrainz.org/ws/2/recording/'


class MusicBrainzTransport(Protocol):
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


class _ArtistCredit(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    name: str


class _Release(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    id: str
    title: str
    artist_credit: tuple[_ArtistCredit, ...] = ()


class _Response(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[_Release, ...]


@dataclass(frozen=True, slots=True)
class MusicBrainzV2Adapter:
    transport: MusicBrainzTransport
    user_agent: str

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        captured_at = now or datetime.now(UTC)
        if request.recording_mbid is not None:
            url = (
                f'{_RECORDING_ENDPOINT}{quote(request.recording_mbid, safe="")}'
                f'?{urlencode({"inc": "releases", "fmt": "json"})}'
            )
            request_key = f'recording:{request.recording_mbid}'
        else:
            url = f'{_ENDPOINT}?{urlencode({"query": request.query, "fmt": "json"})}'
            request_key = f'query:{request.query}'
        response = self.transport.get(url, headers={'User-Agent': self.user_agent, 'Accept': 'application/json'})
        provenance = LiveProvenance(
            'musicbrainz',
            sha256(request_key.encode()).hexdigest(),
            sha256(response.body).hexdigest(),
            response.status_code,
            captured_at,
            'fresh',
            response.body,
        )
        if response.status_code is None:
            return Unavailable(provenance)
        if response.status_code == 429:
            return RateLimited(provenance)
        if response.status_code is not None and response.status_code >= 500:
            return Unavailable(provenance)
        if response.status_code != 200:
            return Malformed(provenance)
        try:
            payload = _Response.model_validate_json(response.body)
        except ValidationError:
            return Malformed(provenance)
        if request.recording_mbid is not None:
            return self._resolve_recording_releases(
                payload.releases,
                request.release_title,
                request.artist_name,
                request.recording_mbid,
                provenance,
            )
        match payload.releases:
            case ():
                return NoMatch(provenance)
            case (release,):
                return MusicBrainzMatch(provenance, self._candidate(release))
            case _:
                return Ambiguous(provenance)

    def _resolve_recording_releases(
        self,
        releases: tuple[_Release, ...],
        release_title: str | None,
        artist_name: str | None,
        recording_mbid: str,
        provenance: LiveProvenance,
    ) -> MusicBrainzResult:
        if release_title is None:
            matching = releases
        else:
            matching = tuple(release for release in releases if release.title.casefold() == release_title.casefold())
        match matching:
            case (release,):
                return MusicBrainzMatch(provenance, self._candidate(release, artist_name, recording_mbid))
            case () if not releases:
                return NoMatch(provenance)
            case _:
                return Ambiguous(provenance)

    @staticmethod
    def _candidate(
        release: _Release, artist_name: str | None = None, recording_mbid: str | None = None
    ) -> ReleaseCandidate:
        artist = artist_name if artist_name is not None else ''.join(item.name for item in release.artist_credit)
        recording_mbids = () if recording_mbid is None else (recording_mbid,)
        return ReleaseCandidate(release.id, release.title, artist, recording_mbids=recording_mbids)
