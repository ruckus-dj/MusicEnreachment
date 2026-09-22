from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from functools import partial
from typing import Protocol
from urllib.parse import quote, urlencode

import anyio
from anyio.to_thread import run_sync
from pydantic import ValidationError

from music_ingest.dto import GenrePage, RecordingResponse, RecordingSearchResponse, Release, ReleaseBrowseResponse
from music_ingest.matching.providers import MusicBrainzHttpResponse

_RELEASE_INCLUDES = 'artist-credits+media+recordings+release-groups+genres+isrcs+artist-rels+labels'


class AsyncMusicBrainzTransport(Protocol):
    async def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


class SyncMusicBrainzTransport(Protocol):
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


MusicBrainzTransport = SyncMusicBrainzTransport


@dataclass(frozen=True, slots=True)
class ThreadedMusicBrainzTransport:
    """Adapt the legacy synchronous provider transport at the only async boundary."""

    transport: SyncMusicBrainzTransport

    async def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        return await run_sync(partial(self.transport.get, url, headers=headers))


@dataclass(frozen=True, slots=True)
class MusicBrainzResponse[T]:
    status_code: int | None
    body: bytes
    payload: T | None


@dataclass(frozen=True, slots=True)
class RecordingDetail:
    recording_mbid: str
    response: MusicBrainzResponse[RecordingResponse]


@dataclass(frozen=True, slots=True)
class ReleaseDetail:
    release_mbid: str
    response: MusicBrainzResponse[Release]


@dataclass(frozen=True, slots=True)
class MusicBrainzClient:
    """Thin typed async client for MusicBrainz WS/2 resources."""

    transport: AsyncMusicBrainzTransport
    user_agent: str
    host: str = 'https://musicbrainz.org'
    concurrency: int = 4
    retry_delay_seconds: float | None = 0.25

    async def search_recordings(self, query: str) -> MusicBrainzResponse[RecordingSearchResponse]:
        response = await self._get('/ws/2/recording/', {'query': query, 'fmt': 'json', 'limit': 25})
        return self._parse(response, RecordingSearchResponse.model_validate_json)

    async def recording_detail(self, recording_mbid: str) -> RecordingDetail:
        offset = 0
        releases: list[Release] = []
        bodies: list[bytes] = []
        while True:
            response = await self._get(
                '/ws/2/release/',
                {'recording': recording_mbid, 'fmt': 'json', 'limit': 100, 'offset': offset},
            )
            bodies.append(response.body)
            page_response = self._parse(response, ReleaseBrowseResponse.model_validate_json)
            page = page_response.payload
            if page is None:
                return RecordingDetail(
                    recording_mbid, MusicBrainzResponse(response.status_code, b'\n'.join(bodies), None)
                )
            releases.extend(page.releases)
            if len(releases) >= page.count:
                return RecordingDetail(
                    recording_mbid,
                    MusicBrainzResponse(
                        response.status_code, b'\n'.join(bodies), RecordingResponse(releases=tuple(releases))
                    ),
                )
            if not page.releases:
                return RecordingDetail(
                    recording_mbid, MusicBrainzResponse(response.status_code, b'\n'.join(bodies), None)
                )
            offset += len(page.releases)

    async def recording_details(self, recording_mbids: tuple[str, ...]) -> dict[str, RecordingDetail]:
        unique_ids = tuple(dict.fromkeys(recording_mbids))
        results: dict[str, RecordingDetail] = {}
        await self._bounded_fan_out(unique_ids, self._recording_task(results))
        return {recording_mbid: results[recording_mbid] for recording_mbid in unique_ids}

    async def release_detail(self, release_mbid: str) -> ReleaseDetail:
        response = await self._get(
            f'/ws/2/release/{quote(release_mbid, safe="")}', {'inc': _RELEASE_INCLUDES, 'fmt': 'json'}
        )
        return ReleaseDetail(release_mbid, self._parse(response, Release.model_validate_json))

    async def release_details(self, release_mbids: tuple[str, ...]) -> dict[str, ReleaseDetail]:
        unique_ids = tuple(dict.fromkeys(release_mbids))
        results: dict[str, ReleaseDetail] = {}
        await self._bounded_fan_out(unique_ids, self._release_task(results))
        return {release_mbid: results[release_mbid] for release_mbid in unique_ids}

    async def genre_page(self, offset: int, limit: int = 100) -> MusicBrainzResponse[GenrePage]:
        response = await self._get('/ws/2/genre/all', {'fmt': 'json', 'limit': limit, 'offset': offset})
        return self._parse(response, GenrePage.model_validate_json)

    def _recording_task(self, results: dict[str, RecordingDetail]) -> Callable[[str], Awaitable[None]]:
        async def fetch(recording_mbid: str) -> None:
            results[recording_mbid] = await self.recording_detail(recording_mbid)

        return fetch

    def _release_task(self, results: dict[str, ReleaseDetail]) -> Callable[[str], Awaitable[None]]:
        async def fetch(release_mbid: str) -> None:
            results[release_mbid] = await self.release_detail(release_mbid)

        return fetch

    async def _bounded_fan_out(self, values: tuple[str, ...], fetch: Callable[[str], Awaitable[None]]) -> None:
        limiter = anyio.CapacityLimiter(self.concurrency)

        async def limited_fetch(value: str) -> None:
            async with limiter:
                await fetch(value)

        async with anyio.create_task_group() as task_group:
            for value in values:
                _ = task_group.start_soon(limited_fetch, value)

    async def _get(self, path: str, params: dict[str, str | int]) -> MusicBrainzHttpResponse:
        url = f'{self.host.rstrip("/")}{path}?{urlencode(params)}'
        headers = {'User-Agent': self.user_agent, 'Accept': 'application/json'}
        if self.retry_delay_seconds is None:
            return await self.transport.get(url, headers=headers)
        for attempt in range(3):
            response = await self.transport.get(url, headers=headers)
            if response.status_code not in {429, 500, 502, 503, 504} or attempt == 2:
                return response
            await anyio.sleep(self.retry_delay_seconds)
        raise AssertionError('unreachable MusicBrainz retry state')

    @staticmethod
    def _parse[T](response: MusicBrainzHttpResponse, parser: Callable[[bytes], T]) -> MusicBrainzResponse[T]:
        if response.status_code != 200:
            return MusicBrainzResponse(response.status_code, response.body, None)
        try:
            payload = parser(response.body)
        except ValidationError:
            return MusicBrainzResponse(response.status_code, response.body, None)
        return MusicBrainzResponse(response.status_code, response.body, payload)
