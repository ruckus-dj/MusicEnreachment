from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from urllib.parse import quote

import anyio

from music_ingest.dto import Release
from music_ingest.enrichment.artwork import ArtworkCandidate, ArtworkFormat
from music_ingest.external.musicbrainz import (
    MusicBrainzClient,
    MusicBrainzResponse,
    SyncMusicBrainzTransport,
    ThreadedMusicBrainzTransport,
)
from music_ingest.matching.musicbrainz_mapping import (
    candidate_for_release,
    merge_recording_candidate,
    title_key,
    without_pseudo_releases,
)
from music_ingest.matching.providers import (
    Ambiguous,
    LiveProvenance,
    Malformed,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    ReleaseCandidate,
    Unavailable,
)


@dataclass(frozen=True, slots=True)
class MusicBrainzProviderAdapter:
    """Resolve typed MusicBrainz API facts into synchronous matching outcomes."""

    client: MusicBrainzClient

    def __init__(
        self, transport: SyncMusicBrainzTransport, user_agent: str, host: str = 'https://musicbrainz.org'
    ) -> None:
        object.__setattr__(
            self,
            'client',
            MusicBrainzClient(ThreadedMusicBrainzTransport(transport), user_agent, host, retry_delay_seconds=None),
        )

    def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
        return anyio.run(self._lookup, request, now or datetime.now(UTC))

    def fetch_artwork(self, release_id: str) -> ArtworkCandidate | None:
        return anyio.run(self._fetch_artwork, release_id)

    async def _lookup(self, request: MusicBrainzLookupRequest, captured_at: datetime) -> MusicBrainzResult:
        if request.release_mbid is not None:
            detail = await self.client.release_detail(request.release_mbid)
            provenance = self._provenance(f'release:{request.release_mbid}', detail.response, captured_at)
            outcome = self._response_outcome(detail.response, provenance)
            if outcome is not None:
                return outcome
            payload = detail.response.payload
            if payload is None:
                return Malformed(provenance)
            return MusicBrainzMatch(
                provenance,
                candidate_for_release(
                    payload,
                    request.artist_name,
                    request.recording_mbid,
                    request.recording_title,
                    request.duration_seconds,
                    request.track_number,
                ),
            )
        if request.recording_mbid is not None:
            return await self._resolve_recordings(
                (request.recording_mbid,), request, captured_at, f'recording:{request.recording_mbid}'
            )
        search = await self.client.search_recordings(request.query)
        provenance = self._provenance(f'query:{request.query}', search, captured_at)
        outcome = self._response_outcome(search, provenance)
        if search.status_code != 200:
            return outcome or Malformed(provenance)
        if request.recording_mbids:
            return await self._resolve_recordings(
                request.recording_mbids, request, captured_at, f'query:{request.query}', provenance
            )
        if outcome is not None:
            legacy = self.client.parse_legacy_releases(search)
            if legacy.payload is None:
                return outcome
            return await self._resolve_legacy_releases(legacy.payload.releases, request, provenance)
        recording_mbids = request.recording_mbids or (
            () if search.payload is None else tuple(item.id for item in search.payload.recordings)
        )
        recording_scores = (
            ()
            if search.payload is None
            else tuple((item.id, item.score) for item in search.payload.recordings if item.score is not None)
        )
        return await self._resolve_recordings(
            recording_mbids, request, captured_at, f'query:{request.query}', provenance, recording_scores
        )

    async def _resolve_legacy_releases(
        self, releases: tuple[Release, ...], request: MusicBrainzLookupRequest, provenance: LiveProvenance
    ) -> MusicBrainzResult:
        eligible = without_pseudo_releases(releases)
        if not eligible:
            return NoMatch(provenance)
        selected_title = None if request.release_title is None else title_key(request.release_title)
        detailed_ids = tuple(
            release.id for release in eligible if selected_title is None or title_key(release.title) == selected_title
        )
        details = await self.client.release_details(detailed_ids)
        enriched: dict[str, Release] = {}
        for detail in details.values():
            if detail.response.payload is not None:
                enriched[detail.release_mbid] = detail.response.payload
                provenance = self._append_provenance(provenance, detail.response)
        candidates = tuple(
            candidate_for_release(
                enriched.get(release.id) or release,
                request.artist_name,
                recording_title=request.recording_title,
                duration_seconds=request.duration_seconds,
                track_number=request.track_number,
            )
            for release in eligible
        )
        return self._candidates_result(candidates, provenance)

    async def _resolve_recordings(
        self,
        recording_mbids: tuple[str, ...],
        request: MusicBrainzLookupRequest,
        captured_at: datetime,
        request_key: str,
        initial_provenance: LiveProvenance | None = None,
        recording_scores: tuple[tuple[str, float], ...] = (),
    ) -> MusicBrainzResult:
        if not recording_mbids:
            return NoMatch(initial_provenance or self._empty_provenance(request_key, captured_at))
        details = await self.client.recording_details(recording_mbids)
        first_detail = next(iter(details.values()))
        provenance = initial_provenance or self._provenance(request_key, first_detail.response, captured_at)
        releases_by_id: dict[str, Release] = {}
        recording_ids_by_release: dict[str, list[str]] = {}
        for detail in details.values():
            outcome = self._response_outcome(
                detail.response, self._provenance(f'recording:{detail.recording_mbid}', detail.response, captured_at)
            )
            if outcome is not None:
                return outcome
            if detail.response.payload is None:
                continue
            provenance = self._provenance(f'recording:{detail.recording_mbid}', detail.response, captured_at)
            for release in without_pseudo_releases(detail.response.payload.releases):
                _ = releases_by_id.setdefault(release.id, release)
                recording_ids_by_release.setdefault(release.id, []).append(detail.recording_mbid)
        if not releases_by_id:
            return NoMatch(provenance)
        release_details = await self.client.release_details(tuple(releases_by_id))
        enriched = dict(releases_by_id)
        for detail in release_details.values():
            if detail.response.payload is not None:
                enriched[detail.release_mbid] = detail.response.payload
                provenance = self._append_provenance(provenance, detail.response)
        candidates: dict[str, ReleaseCandidate] = {}
        search_scores = dict(recording_scores)
        for release_id, matched_recording_ids in recording_ids_by_release.items():
            for recording_mbid in dict.fromkeys(matched_recording_ids):
                candidate = candidate_for_release(
                    enriched[release_id],
                    request.artist_name,
                    recording_mbid,
                    request.recording_title,
                    request.duration_seconds,
                    request.track_number,
                    search_scores.get(recording_mbid),
                )
                existing = candidates.get(release_id)
                candidates[release_id] = (
                    candidate if existing is None else merge_recording_candidate(existing, candidate, request)
                )
        return self._candidates_result(tuple(candidates.values()), provenance)

    async def _fetch_artwork(self, release_id: str) -> ArtworkCandidate | None:
        response = await self.client.transport.get(
            f'https://coverartarchive.org/release/{quote(release_id, safe="")}/front-500',
            headers={'User-Agent': self.client.user_agent, 'Accept': 'image/jpeg,image/webp'},
        )
        if response.status_code != 200:
            return None
        if response.body.startswith(b'\xff\xd8\xff') and response.body.endswith(b'\xff\xd9'):
            return ArtworkCandidate(release_id, ArtworkFormat.JPEG, response.body)
        if len(response.body) >= 12 and response.body[:4] == b'RIFF' and response.body[8:12] == b'WEBP':
            return ArtworkCandidate(release_id, ArtworkFormat.WEBP, response.body)
        return None

    @staticmethod
    def _candidates_result(candidates: tuple[ReleaseCandidate, ...], provenance: LiveProvenance) -> MusicBrainzResult:
        match candidates:
            case ():
                return NoMatch(provenance)
            case (candidate,):
                return MusicBrainzMatch(provenance, candidate)
            case multiple:
                return Ambiguous(provenance, multiple)

    @staticmethod
    def _response_outcome[T](response: MusicBrainzResponse[T], provenance: LiveProvenance) -> MusicBrainzResult | None:
        if response.status_code is None:
            return Unavailable(provenance)
        if response.status_code == 429:
            return RateLimited(provenance)
        if response.status_code >= 500:
            return Unavailable(provenance)
        if response.status_code != 200 or response.payload is None:
            return Malformed(provenance)
        return None

    @staticmethod
    def _provenance[T](request_key: str, response: MusicBrainzResponse[T], captured_at: datetime) -> LiveProvenance:
        return LiveProvenance(
            'musicbrainz',
            sha256(request_key.encode()).hexdigest(),
            sha256(response.body).hexdigest(),
            response.status_code,
            captured_at,
            'fresh',
            response.body,
        )

    @staticmethod
    def _empty_provenance(request_key: str, captured_at: datetime) -> LiveProvenance:
        return LiveProvenance(
            'musicbrainz', sha256(request_key.encode()).hexdigest(), sha256(b'').hexdigest(), None, captured_at, 'fresh'
        )

    @staticmethod
    def _append_provenance[T](provenance: LiveProvenance, response: MusicBrainzResponse[T]) -> LiveProvenance:
        body = provenance.response_body + b'\n' + response.body
        return LiveProvenance(
            provenance.provider_name,
            provenance.request_hash,
            sha256(body).hexdigest(),
            response.status_code,
            provenance.captured_at,
            provenance.state,
            body,
        )
