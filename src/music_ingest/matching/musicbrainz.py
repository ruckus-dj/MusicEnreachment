from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import UTC, datetime
from hashlib import sha256
from typing import ClassVar, Protocol
from unicodedata import normalize
from urllib.parse import quote, urlencode

from pydantic import BaseModel, ConfigDict, Field, ValidationError

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


class _Genre(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    name: str


class _ArtistCredit(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    name: str
    genres: tuple[_Genre, ...] = ()


class _ReleaseGroup(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    first_release_date: str | None = Field(default=None, alias='first-release-date')


class _TrackRecording(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    id: str
    title: str
    artist_credit: tuple[_ArtistCredit, ...] = ()
    genres: tuple[_Genre, ...] = ()


class _Track(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    position: int
    title: str
    length: int | None = None
    recording: _TrackRecording


class _Medium(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    position: int
    track_count: int | None = Field(default=None, alias='track-count')
    tracks: tuple[_Track, ...] = ()


class _Release(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    title: str
    status: str | None = None
    artist_credit: tuple[_ArtistCredit, ...] = ()
    date: str | None = None
    genres: tuple[_Genre, ...] = ()
    release_group: _ReleaseGroup | None = Field(default=None, alias='release-group')
    media: tuple[_Medium, ...] = ()


class _Response(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[_Release, ...]


class _RecordingResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[_Release, ...]


@dataclass(frozen=True, slots=True)
class MusicBrainzV2Adapter:
    transport: MusicBrainzTransport
    user_agent: str

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        captured_at = now or datetime.now(UTC)
        if request.release_mbid is not None:
            url = (
                f'{_ENDPOINT}{quote(request.release_mbid, safe="")}'
                f'?{urlencode({"inc": "artist-credits+media+recordings+release-groups+genres", "fmt": "json"})}'
            )
            request_key = f'release:{request.release_mbid}'
        elif request.recording_mbid is not None:
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
        if response.status_code >= 500:
            return Unavailable(provenance)
        if response.status_code != 200:
            return Malformed(provenance)
        if request.release_mbid is not None:
            try:
                release_payload = _Release.model_validate_json(response.body)
            except ValidationError:
                return Malformed(provenance)
            return MusicBrainzMatch(
                provenance,
                self._candidate(release_payload, request.artist_name, request.recording_mbid),
            )
        if request.recording_mbid is not None:
            try:
                recording_payload = _RecordingResponse.model_validate_json(response.body)
            except ValidationError:
                return Malformed(provenance)
            return self._resolve_recording_releases(
                recording_payload.releases,
                request.release_title,
                request.artist_name,
                request.recording_mbid,
                provenance,
            )
        try:
            search_payload = _Response.model_validate_json(response.body)
        except ValidationError:
            return Malformed(provenance)
        eligible = _without_pseudo_releases(search_payload.releases)
        match eligible:
            case ():
                return NoMatch(provenance)
            case (release,):
                enriched, enriched_provenance = self._enrich_release(release, provenance)
                return MusicBrainzMatch(enriched_provenance, self._candidate(enriched))
            case _:
                return Ambiguous(provenance, tuple(self._candidate(release) for release in eligible))

    def _resolve_recording_releases(
        self,
        releases: tuple[_Release, ...],
        release_title: str | None,
        artist_name: str | None,
        recording_mbid: str,
        provenance: LiveProvenance,
    ) -> MusicBrainzResult:
        eligible = _without_pseudo_releases(releases)
        matching = _matching_releases(eligible, release_title)
        match matching:
            case (release,):
                enriched, enriched_provenance = self._enrich_release(release, provenance)
                return MusicBrainzMatch(
                    enriched_provenance,
                    self._candidate(enriched, artist_name, recording_mbid),
                )
            case () if not eligible:
                return NoMatch(provenance)
            case _:
                enriched_releases: list[_Release] = []
                enriched_provenance = provenance
                for release in matching:
                    enriched, enriched_provenance = self._enrich_release(release, enriched_provenance)
                    enriched_releases.append(enriched)
                return Ambiguous(
                    enriched_provenance,
                    tuple(self._candidate(release, artist_name, recording_mbid) for release in enriched_releases),
                )

    def _enrich_release(
        self,
        release: _Release,
        provenance: LiveProvenance,
    ) -> tuple[_Release, LiveProvenance]:
        url = (
            f'{_ENDPOINT}{quote(release.id, safe="")}'
            f'?{urlencode({"inc": "artist-credits+media+recordings+release-groups+genres", "fmt": "json"})}'
        )
        response = self.transport.get(url, headers={'User-Agent': self.user_agent, 'Accept': 'application/json'})
        if response.status_code != 200:
            return release, provenance
        try:
            detailed = _Release.model_validate_json(response.body)
        except ValidationError:
            return release, provenance
        combined_body = provenance.response_body + b'\n' + response.body
        return detailed, replace(
            provenance,
            sha256=sha256(combined_body).hexdigest(),
            response_body=combined_body,
        )

    @staticmethod
    def _candidate(
        release: _Release, artist_name: str | None = None, recording_mbid: str | None = None
    ) -> ReleaseCandidate:
        track = next(
            (
                track
                for medium in release.media
                for track in medium.tracks
                if recording_mbid is None or track.recording.id == recording_mbid
            ),
            None,
        )
        artist = artist_name if artist_name is not None else ''.join(item.name for item in release.artist_credit)
        if not artist and track is not None:
            artist = ''.join(item.name for item in track.recording.artist_credit)
        recording_mbids = () if recording_mbid is None else (recording_mbid,)
        medium = next(
            (medium for medium in release.media if track is not None and track in medium.tracks),
            None,
        )
        track_total = medium.track_count if medium is not None else None
        if medium is not None and track_total is None:
            track_total = len(medium.tracks)
        return ReleaseCandidate(
            release.id,
            release.title,
            artist,
            duration_seconds=None if track is None or track.length is None else round(track.length / 1000),
            recording_mbids=recording_mbids,
            recording_title=None if track is None else track.recording.title,
            date=release.date,
            original_date=None if release.release_group is None else release.release_group.first_release_date,
            track_number=None if track is None else track.position,
            track_total=track_total,
            disc_number=None if medium is None else medium.position,
            disc_total=len(release.media) if release.media else None,
            genres=select_genres(
                tuple(genre.name for genre in track.recording.genres) if track is not None else (),
                tuple(genre.name for genre in release.genres),
                tuple(genre.name for credit in release.artist_credit for genre in credit.genres),
            ),
            release_group_mbid=None if release.release_group is None else release.release_group.id,
        )


def _matching_releases(releases: tuple[_Release, ...], release_title: str | None) -> tuple[_Release, ...]:
    if release_title is None:
        return releases
    requested = _title_key(release_title)
    exact = tuple(release for release in releases if _title_key(release.title) == requested)
    return exact or releases


def _without_pseudo_releases(releases: tuple[_Release, ...]) -> tuple[_Release, ...]:
    return tuple(release for release in releases if (release.status or '').casefold() != 'pseudo-release')


def select_genres(
    track_genres: tuple[str, ...], album_genres: tuple[str, ...], artist_genres: tuple[str, ...]
) -> tuple[str, ...]:
    for genres in (track_genres, album_genres, artist_genres):
        selected = tuple(dict.fromkeys(genre.strip() for genre in genres if genre.strip()))
        if selected:
            return selected
    return ()


def _title_key(value: str) -> str:
    return ''.join(character for character in normalize('NFKC', value).casefold() if character.isalnum())
