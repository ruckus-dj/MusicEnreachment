from __future__ import annotations

from dataclasses import dataclass
from typing import override

import anyio

from music_ingest.adapters.external.musicbrainz import (
    MusicBrainzClient,
    SyncMusicBrainzTransport,
    ThreadedMusicBrainzTransport,
)
from music_ingest.services.normalize.genre_names import display_genre_name

_PAGE_SIZE = 100


@dataclass(frozen=True, slots=True)
class GenreCatalogEntry:
    musicbrainz_id: str
    source_name: str
    display_name: str


@dataclass(frozen=True, slots=True)
class GenreCatalogSyncError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def sync_genres(
    transport: SyncMusicBrainzTransport, *, user_agent: str, host: str = 'https://musicbrainz.org'
) -> tuple[GenreCatalogEntry, ...]:
    return anyio.run(_sync_genres, MusicBrainzClient(ThreadedMusicBrainzTransport(transport), user_agent, host))


async def _sync_genres(client: MusicBrainzClient) -> tuple[GenreCatalogEntry, ...]:
    entries: list[GenreCatalogEntry] = []
    offset = 0
    while True:
        response = await client.genre_page(offset, _PAGE_SIZE)
        if response.status_code != 200 or response.payload is None:
            raise GenreCatalogSyncError(f'MusicBrainz genre catalog returned HTTP {response.status_code}')
        page = response.payload
        if page.offset != offset:
            raise GenreCatalogSyncError('MusicBrainz genre catalog page offset was inconsistent')
        if offset < page.count and not page.genres:
            raise GenreCatalogSyncError('MusicBrainz genre catalog ended before its reported count')
        if offset + len(page.genres) > page.count:
            raise GenreCatalogSyncError('MusicBrainz genre catalog exceeded its reported count')
        if offset + len(page.genres) < page.count and len(page.genres) != _PAGE_SIZE:
            raise GenreCatalogSyncError('MusicBrainz genre catalog page was truncated')
        known_ids = {entry.musicbrainz_id for entry in entries}
        known_names = {entry.source_name for entry in entries}
        page_ids = tuple(item.musicbrainz_id for item in page.genres)
        page_names = tuple(item.source_name for item in page.genres)
        if (
            len(set(page_ids)) != len(page_ids)
            or len(set(page_names)) != len(page_names)
            or any(item.musicbrainz_id in known_ids or item.source_name in known_names for item in page.genres)
        ):
            raise GenreCatalogSyncError('MusicBrainz genre catalog contained duplicate entries')
        entries.extend(
            GenreCatalogEntry(item.musicbrainz_id, item.source_name, display_genre_name(item.source_name))
            for item in page.genres
        )
        offset += len(page.genres)
        if not page.genres or offset >= page.count:
            break
    return tuple(sorted(entries, key=lambda item: item.source_name.casefold()))
