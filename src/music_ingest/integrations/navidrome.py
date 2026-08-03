from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from time import monotonic, sleep
from typing import ClassVar, override

from pydantic import BaseModel, ConfigDict, Field


class IndexedArtist(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    name: str


class SearchResult(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    artists: tuple[IndexedArtist, ...] = Field(default=(), alias='artist')


class SearchPayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    result: SearchResult = Field(alias='searchResult3')


class SearchResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    payload: SearchPayload = Field(alias='subsonic-response')


class IndexedGenre(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    value: str


class Genres(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    values: tuple[IndexedGenre, ...] = Field(default=(), alias='genre')


class GenrePayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    genres: Genres


class GenreResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    payload: GenrePayload = Field(alias='subsonic-response')


class ScanStatus(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    scanning: bool


class ScanPayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    status: ScanStatus = Field(alias='scanStatus')


class ScanResponse(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    payload: ScanPayload = Field(alias='subsonic-response')


@dataclass(frozen=True, slots=True)
class ScanPollingPolicy:
    timeout_seconds: float = 30.0
    interval_seconds: float = 0.1


@dataclass(frozen=True, slots=True)
class NavidromeScanTimeout(Exception):
    timeout_seconds: float

    @override
    def __str__(self) -> str:
        return f'Navidrome scan did not complete within {self.timeout_seconds} seconds'


def wait_for_scan_completion(
    fetch_status: Callable[[], ScanStatus], policy: ScanPollingPolicy | None = None
) -> ScanStatus:
    active_policy = policy or ScanPollingPolicy()
    deadline = monotonic() + active_policy.timeout_seconds
    status = fetch_status()
    while status.scanning:
        if monotonic() >= deadline:
            raise NavidromeScanTimeout(active_policy.timeout_seconds)
        sleep(active_policy.interval_seconds)
        status = fetch_status()
    return status


def catalog_matches(
    artist_searches: tuple[SearchResponse, ...],
    expected_artists: tuple[str, ...],
    genres: GenreResponse,
    expected_genres: tuple[str, ...],
) -> bool:
    if len(artist_searches) != len(expected_artists):
        return False
    artist_matches = all(
        any(artist.name.casefold() == expected.casefold() for artist in response.payload.result.artists)
        for response, expected in zip(artist_searches, expected_artists, strict=True)
    )
    indexed_genres = {genre.value.casefold() for genre in genres.payload.genres.values}
    return artist_matches and all(expected.casefold() in indexed_genres for expected in expected_genres)
