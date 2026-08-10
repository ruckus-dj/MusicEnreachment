from typing import ClassVar

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
