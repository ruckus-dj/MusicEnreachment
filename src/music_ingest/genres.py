from __future__ import annotations

import re
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime
from typing import Protocol
from urllib.parse import urlencode

from pydantic import BaseModel, ConfigDict, Field, ValidationError
from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from music_ingest.matching.providers import MusicBrainzHttpResponse
from music_ingest.persistence.models import GenreCatalogRecord

_ENDPOINT = 'https://musicbrainz.org/ws/2/genre/all'
_PAGE_SIZE = 100
_SPECIAL_LABELS = {
    'dnb': 'DnB',
    'edm': 'EDM',
    'idm': 'IDM',
    'j-pop': 'J-Pop',
    'k-pop': 'K-Pop',
    'r&b': 'R&B',
}
_GENRE_TEXT = re.compile(r'[^\w]+', re.UNICODE)


class GenreTransport(Protocol):
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse: ...


@dataclass(frozen=True, slots=True)
class GenreCatalogEntry:
    musicbrainz_id: str
    source_name: str
    display_name: str


class GenreCatalogSyncError(RuntimeError):
    """Raised when MusicBrainz returns an unusable genre catalog response."""


class _GenrePayload(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_id: str = Field(alias='id', min_length=1)
    source_name: str = Field(alias='name', min_length=1)


class _GenrePage(BaseModel):
    model_config = ConfigDict(frozen=True)

    count: int = Field(alias='genre-count', ge=0)
    offset: int = Field(alias='genre-offset', ge=0)
    genres: tuple[_GenrePayload, ...]


def sync_genres(
    transport: GenreTransport,
    *,
    user_agent: str,
    sleep: Callable[[float], None] = time.sleep,
) -> tuple[GenreCatalogEntry, ...]:
    """Fetch the complete alphabetized MusicBrainz genre catalog at one request per second."""
    entries: list[GenreCatalogEntry] = []
    offset = 0
    while True:
        query = urlencode({'fmt': 'json', 'limit': _PAGE_SIZE, 'offset': offset})
        response = transport.get(
            f'{_ENDPOINT}?{query}',
            headers={'User-Agent': user_agent, 'Accept': 'application/json'},
        )
        if response.status_code != 200:
            raise GenreCatalogSyncError(f'MusicBrainz genre catalog returned HTTP {response.status_code}')
        try:
            page = _GenrePage.model_validate_json(response.body)
        except ValidationError as error:
            raise GenreCatalogSyncError('MusicBrainz genre catalog response was invalid') from error
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
        sleep(1.0)
    return tuple(sorted(entries, key=lambda item: item.source_name.casefold()))


def display_genre_name(source_name: str) -> str:
    """Convert a MusicBrainz source name into a readable UI label without changing its key."""
    special = _SPECIAL_LABELS.get(source_name.casefold())
    if special is not None:
        return special
    return ' '.join(_display_word(word) for word in re.split(r'[\s-]+', source_name.strip()) if word)


def genre_key(value: str) -> str:
    """Return the stable comparison key used for source names and tags."""
    return _GENRE_TEXT.sub(' ', value.casefold()).strip()


def replace_genre_catalog(session: Session, entries: tuple[GenreCatalogEntry, ...], synced_at: datetime) -> None:
    """Replace the persisted catalog only after a complete upstream sync succeeds."""
    session.execute(delete(GenreCatalogRecord))
    session.add_all(
        GenreCatalogRecord(
            musicbrainz_id=entry.musicbrainz_id,
            source_name=entry.source_name,
            display_name=entry.display_name,
            normalized_key=genre_key(entry.source_name),
            synced_at=synced_at,
        )
        for entry in entries
    )


def load_genre_catalog(session: Session) -> tuple[GenreCatalogRecord, ...]:
    """Return the stored MusicBrainz catalog in display order."""
    return tuple(session.scalars(select(GenreCatalogRecord).order_by(GenreCatalogRecord.display_name)).all())


def _display_word(word: str) -> str:
    return word[:1].upper() + word[1:]
