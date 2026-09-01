from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, replace
from datetime import UTC, datetime
from hashlib import sha256
from typing import Protocol
from unicodedata import normalize
from urllib.parse import quote, urlencode

from pydantic import ValidationError
from rapidfuzz.fuzz import ratio

from music_ingest.dto import (
    LabelInfo,
    RecordingResponse,
    RecordingSearchResponse,
    RecordingSearchResult,
    Release,
    ReleaseResponse,
)
from music_ingest.dto.api import Track
from music_ingest.enrichment.artwork import ArtworkCandidate, ArtworkFormat
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

_COVER_ART_ENDPOINT = 'https://coverartarchive.org/release/'
_RELEASE_INCLUDES = 'artist-credits+media+recordings+release-groups+genres+isrcs+artist-rels+labels'
_RECORDING_SEARCH_CONCURRENCY = 4


class MusicBrainzTransport(Protocol):
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


@dataclass(frozen=True, slots=True)
class _RecordingLookup:
    recording_mbid: str
    releases: tuple[Release, ...]
    provenance: LiveProvenance


@dataclass(frozen=True, slots=True)
class MusicBrainzV2Adapter:
    transport: MusicBrainzTransport
    user_agent: str
    host: str = 'https://musicbrainz.org'

    def fetch_artwork(self, release_id: str) -> ArtworkCandidate | None:
        """Fetch the first verified front cover for a MusicBrainz release."""
        response = self.transport.get(
            f'{_COVER_ART_ENDPOINT}{quote(release_id, safe="")}/front-500',
            headers={'User-Agent': self.user_agent, 'Accept': 'image/jpeg,image/webp'},
        )
        if response.status_code != 200:
            return None
        if response.body.startswith(b'\xff\xd8\xff') and response.body.endswith(b'\xff\xd9'):
            return ArtworkCandidate(release_id, ArtworkFormat.JPEG, response.body)
        if len(response.body) >= 12 and response.body[:4] == b'RIFF' and response.body[8:12] == b'WEBP':
            return ArtworkCandidate(release_id, ArtworkFormat.WEBP, response.body)
        return None

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        captured_at = now or datetime.now(UTC)
        if request.release_mbid is not None:
            url = (
                f'{self._release_endpoint}{quote(request.release_mbid, safe="")}'
                f'?{urlencode({"inc": _RELEASE_INCLUDES, "fmt": "json"})}'
            )
            request_key = f'release:{request.release_mbid}'
        elif request.query and request.recording_mbids:
            url = f'{self._recording_endpoint}?{urlencode({"query": request.query, "fmt": "json", "limit": 25})}'
            request_key = f'query:{request.query}'
        elif request.recording_mbid is not None:
            url = (
                f'{self._recording_endpoint}{quote(request.recording_mbid, safe="")}'
                f'?{urlencode({"inc": "releases", "fmt": "json"})}'
            )
            request_key = f'recording:{request.recording_mbid}'
        else:
            url = f'{self._recording_endpoint}?{urlencode({"query": request.query, "fmt": "json", "limit": 25})}'
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
                release_payload = Release.model_validate_json(response.body)
            except ValidationError:
                return Malformed(provenance)
            return MusicBrainzMatch(
                provenance,
                self._candidate(
                    release_payload,
                    request.artist_name,
                    request.recording_mbid,
                    request.recording_title,
                    request.duration_seconds,
                    request.track_number,
                ),
            )
        if request.query and request.recording_mbids:
            try:
                search_payload = RecordingSearchResponse.model_validate_json(response.body)
            except ValidationError:
                try:
                    legacy_payload = ReleaseResponse.model_validate_json(response.body)
                except ValidationError:
                    return Malformed(provenance)
                if request.recording_mbids:
                    return self._resolve_recording_search((), request, provenance, captured_at)
                return self._resolve_legacy_release_search(legacy_payload, request, provenance)
            return self._resolve_recording_search(search_payload.recordings, request, provenance, captured_at)
        if request.recording_mbid is not None:
            try:
                recording_payload = RecordingResponse.model_validate_json(response.body)
            except ValidationError:
                return Malformed(provenance)
            return self._resolve_recording_releases(
                recording_payload.releases,
                request.artist_name,
                request.recording_mbid,
                provenance,
            )
        try:
            search_payload = RecordingSearchResponse.model_validate_json(response.body)
        except ValidationError:
            try:
                legacy_payload = ReleaseResponse.model_validate_json(response.body)
            except ValidationError:
                return Malformed(provenance)
            return self._resolve_legacy_release_search(legacy_payload, request, provenance)
        return self._resolve_recording_search(search_payload.recordings, request, provenance, captured_at)

    def _resolve_recording_releases(
        self,
        releases: tuple[Release, ...],
        artist_name: str | None,
        recording_mbid: str,
        provenance: LiveProvenance,
    ) -> MusicBrainzResult:
        eligible = _without_pseudo_releases(releases)
        match eligible:
            case (release,):
                enriched, enriched_provenance = self._enrich_release(release, provenance)
                return MusicBrainzMatch(
                    enriched_provenance,
                    self._candidate(enriched, artist_name, recording_mbid),
                )
            case () if not eligible:
                return NoMatch(provenance)
            case _:
                enriched_by_id, enriched_provenance = self._enrich_release_set(eligible, provenance)
                enriched_releases = tuple(enriched_by_id[release.id] for release in eligible)
                candidates = tuple(
                    self._candidate(release, artist_name, recording_mbid) for release in enriched_releases
                )
                return Ambiguous(
                    enriched_provenance,
                    candidates,
                )

    def _resolve_recording_search(
        self,
        recordings: tuple[RecordingSearchResult, ...],
        request: MusicBrainzLookupRequest,
        provenance: LiveProvenance,
        now: datetime,
    ) -> MusicBrainzResult:
        recording_ids = request.recording_mbids or tuple(recording.id for recording in recordings)
        if not recording_ids:
            return NoMatch(provenance)
        with ThreadPoolExecutor(max_workers=min(_RECORDING_SEARCH_CONCURRENCY, len(recording_ids))) as executor:
            futures = tuple(
                executor.submit(self._lookup_recording, recording_id, now) for recording_id in recording_ids
            )
            recording_lookups = tuple(future.result() for future in futures)
        available_lookups = tuple(lookup for lookup in recording_lookups if lookup is not None)
        if not available_lookups:
            return NoMatch(provenance)

        releases_by_id: dict[str, Release] = {}
        recording_ids_by_release: dict[str, list[str]] = {}
        enriched_provenance = provenance
        for lookup in available_lookups:
            enriched_provenance = lookup.provenance
            for release in _without_pseudo_releases(lookup.releases):
                releases_by_id.setdefault(release.id, release)
                recording_ids = recording_ids_by_release.setdefault(release.id, [])
                if lookup.recording_mbid not in recording_ids:
                    recording_ids.append(lookup.recording_mbid)
        if not releases_by_id:
            return NoMatch(enriched_provenance)
        enriched_by_id, enriched_provenance = self._enrich_release_set(
            tuple(releases_by_id.values()), enriched_provenance
        )
        candidates: list[ReleaseCandidate] = []
        for release_id, recording_ids in recording_ids_by_release.items():
            enriched = enriched_by_id[release_id]
            candidates.extend(
                self._candidate(
                    enriched,
                    request.artist_name,
                    recording_mbid=recording_mbid,
                    recording_title=request.recording_title,
                    duration_seconds=request.duration_seconds,
                    track_number=request.track_number,
                )
                for recording_mbid in recording_ids
            )
        unique: dict[str, ReleaseCandidate] = {}
        for candidate in candidates:
            existing = unique.get(candidate.release_mbid)
            if existing is None:
                unique[candidate.release_mbid] = candidate
                continue
            candidate_score = _recording_match_score(candidate, request)
            existing_score = _recording_match_score(existing, request)
            if request.recording_title is not None and candidate_score > existing_score:
                unique[candidate.release_mbid] = candidate
                continue
            unique[candidate.release_mbid] = replace(
                existing,
                recording_mbids=tuple(dict.fromkeys((*existing.recording_mbids, *candidate.recording_mbids))),
            )
        unique_candidates = tuple(unique.values())
        match unique_candidates:
            case ():
                return NoMatch(enriched_provenance)
            case (candidate,):
                return MusicBrainzMatch(enriched_provenance, candidate)
            case multiple_candidates:
                return Ambiguous(enriched_provenance, multiple_candidates)

    def _lookup_recording(self, recording_mbid: str, now: datetime) -> _RecordingLookup | None:
        response = self.transport.get(
            f'{self._recording_endpoint}{quote(recording_mbid, safe="")}'
            f'?{urlencode({"inc": "releases", "fmt": "json"})}',
            headers={'User-Agent': self.user_agent, 'Accept': 'application/json'},
        )
        provenance = LiveProvenance(
            'musicbrainz',
            sha256(f'recording:{recording_mbid}'.encode()).hexdigest(),
            sha256(response.body).hexdigest(),
            response.status_code,
            now,
            'fresh',
            response.body,
        )
        if response.status_code != 200:
            return None
        try:
            payload = RecordingResponse.model_validate_json(response.body)
        except ValidationError:
            return None
        return _RecordingLookup(recording_mbid, payload.releases, provenance)

    def _enrich_release_set(
        self, releases: tuple[Release, ...], provenance: LiveProvenance
    ) -> tuple[dict[str, Release], LiveProvenance]:
        unique_releases: dict[str, Release] = {}
        for release in releases:
            unique_releases.setdefault(release.id, release)
        with ThreadPoolExecutor(max_workers=min(_RECORDING_SEARCH_CONCURRENCY, len(unique_releases))) as executor:
            futures = tuple(
                executor.submit(self._enrich_release, release, provenance) for release in unique_releases.values()
            )
            results = tuple(future.result() for future in futures)
        return (
            {release.id: release for release, _ in results},
            results[-1][1] if results else provenance,
        )

    def _resolve_legacy_release_search(
        self,
        payload: ReleaseResponse,
        request: MusicBrainzLookupRequest,
        provenance: LiveProvenance,
    ) -> MusicBrainzResult:
        eligible = _without_pseudo_releases(payload.releases)
        match eligible:
            case ():
                return NoMatch(provenance)
            case (release,):
                enriched, enriched_provenance = self._enrich_release(release, provenance)
                return MusicBrainzMatch(
                    enriched_provenance,
                    self._candidate(
                        enriched,
                        request.artist_name,
                        recording_title=request.recording_title,
                        duration_seconds=request.duration_seconds,
                        track_number=request.track_number,
                    ),
                )
            case _:
                enriched_provenance = provenance
                candidates: list[ReleaseCandidate] = []
                requested_title = None if request.release_title is None else _title_key(request.release_title)
                for release in eligible:
                    if requested_title is not None and _title_key(release.title) == requested_title:
                        enriched, enriched_provenance = self._enrich_release(release, enriched_provenance)
                    else:
                        enriched = release
                    candidates.append(
                        self._candidate(
                            enriched,
                            request.artist_name,
                            recording_title=request.recording_title,
                            duration_seconds=request.duration_seconds,
                            track_number=request.track_number,
                        )
                    )
                return Ambiguous(enriched_provenance, tuple(candidates))

    def _enrich_release(
        self,
        release: Release,
        provenance: LiveProvenance,
    ) -> tuple[Release, LiveProvenance]:
        query = urlencode({'inc': _RELEASE_INCLUDES, 'fmt': 'json'})
        url = f'{self._release_endpoint}{quote(release.id, safe="")}?{query}'
        response = self.transport.get(url, headers={'User-Agent': self.user_agent, 'Accept': 'application/json'})
        if response.status_code != 200:
            return release, provenance
        try:
            detailed = Release.model_validate_json(response.body)
        except ValidationError:
            return release, provenance
        combined_body = provenance.response_body + b'\n' + response.body
        return detailed, replace(
            provenance,
            sha256=sha256(combined_body).hexdigest(),
            response_body=combined_body,
        )

    @property
    def _release_endpoint(self) -> str:
        return f'{self.host}/ws/2/release/'

    @property
    def _recording_endpoint(self) -> str:
        return f'{self.host}/ws/2/recording/'

    @staticmethod
    def _candidate(
        release: Release,
        artist_name: str | None = None,
        recording_mbid: str | None = None,
        recording_title: str | None = None,
        duration_seconds: int | None = None,
        track_number: int | None = None,
    ) -> ReleaseCandidate:
        release_tracks = tuple(track for medium in release.media for track in medium.tracks)
        if recording_mbid is not None:
            track = next((track for track in release_tracks if track.recording.id == recording_mbid), None)
        elif recording_title is not None:
            track = max(
                release_tracks,
                key=lambda item: _track_match_score(item, recording_title, duration_seconds, track_number),
                default=None,
            )
        else:
            track = release_tracks[0] if len(release_tracks) == 1 else None
        release_artist_name = ''.join(f'{item.name}{item.joinphrase}' for item in release.artist_credit)
        artist = artist_name if artist_name is not None else release_artist_name
        if not artist and track is not None:
            artist = ''.join(f'{item.name}{item.joinphrase}' for item in track.recording.artist_credit)
        recording_mbids = (
            (recording_mbid,)
            if recording_mbid is not None
            else (track.recording.id,)
            if recording_title is not None and track is not None
            else (track.recording.id,)
            if len(release_tracks) == 1 and track is not None
            else ()
        )
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
            disambiguation=getattr(release, 'disambiguation', None),
            duration_seconds=None if track is None or track.length is None else round(track.length / 1000),
            recording_mbids=recording_mbids,
            recording_title=None if track is None else track.recording.title,
            date=release.date,
            original_date=None if release.release_group is None else release.release_group.first_release_date,
            country=release.country,
            track_number=None if track is None else track.position,
            track_total=track_total,
            disc_number=None if medium is None else medium.position,
            disc_total=len(release.media) if release.media else None,
            genres=select_genres(
                tuple(genre.name for genre in track.recording.genres) if track is not None else (),
                tuple(genre.name for genre in release.genres),
                tuple(
                    genre.name
                    for credit in release.artist_credit
                    for artist in (credit.artist,)
                    if artist is not None
                    for genre in artist.genres
                ),
            ),
            release_group_mbid=None if release.release_group is None else release.release_group.id,
            isrcs=() if track is None else track.recording.isrcs,
            performers=()
            if track is None
            else tuple(
                relation.artist.name
                for relation in track.recording.relations
                if relation.target_type == 'artist'
                and relation.type in {'performer', 'vocal', 'instrument'}
                and relation.artist is not None
            ),
            release_artist_name=release_artist_name or None,
            recording_artist_names=(
                () if track is None else tuple(credit.name for credit in track.recording.artist_credit)
            ),
            release_artist_names=tuple(credit.name for credit in release.artist_credit),
            recording_artist_mbids=(
                ()
                if track is None or any(credit.artist is None for credit in track.recording.artist_credit)
                else tuple(credit.artist.id for credit in track.recording.artist_credit if credit.artist is not None)
            ),
            release_artist_mbids=(
                ()
                if any(credit.artist is None for credit in release.artist_credit)
                else tuple(credit.artist.id for credit in release.artist_credit if credit.artist is not None)
            ),
            catalog_numbers=_catalog_numbers(release),
        )


def _track_match_score(track: Track, title: str, duration_seconds: int | None, track_number: int | None) -> float:
    title_score = ratio(_title_key(title), _title_key(track.recording.title)) / 100
    duration_score = (
        0.0
        if duration_seconds is None or track.length is None
        else max(0.0, 1.0 - abs(duration_seconds - round(track.length / 1000)) / 10)
    )
    number_score = 1.0 if track_number is not None and track.position == track_number else 0.0
    return 0.6 * title_score + 0.25 * duration_score + 0.15 * number_score


def _recording_match_score(candidate: ReleaseCandidate, request: MusicBrainzLookupRequest) -> float:
    title_score = ratio(_title_key(request.recording_title or ''), _title_key(candidate.recording_title or '')) / 100
    duration_score = (
        0.0
        if request.duration_seconds is None or candidate.duration_seconds is None
        else max(0.0, 1.0 - abs(request.duration_seconds - candidate.duration_seconds) / 10)
    )
    number_score = 1.0 if request.track_number is not None and candidate.track_number == request.track_number else 0.0
    return 0.6 * title_score + 0.25 * duration_score + 0.15 * number_score


def _catalog_numbers(release: Release) -> tuple[str, ...]:
    label_info: tuple[LabelInfo, ...] = release.label_info
    return tuple(number for number in (release.barcode, *(item.catalog_number for item in label_info)) if number)


def _without_pseudo_releases(releases: tuple[Release, ...]) -> tuple[Release, ...]:
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
