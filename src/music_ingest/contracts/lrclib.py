from typing import ClassVar

from pydantic import BaseModel, ConfigDict, Field


class RecordPayload(BaseModel):
    """One LRCLIB ``/api/get`` record; unknown provider fields are ignored.

    Only ``syncedLyrics`` is modelled. The provider's ``plainLyrics`` is deliberately absent, so an unsynced
    payload can never be read as a usable lyric source by accident; ``extra='ignore'`` drops it from the wire
    format without ever exposing it to callers.
    """

    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: int | None = None
    track_name: str | None = Field(default=None, alias='trackName')
    artist_name: str | None = Field(default=None, alias='artistName')
    album_name: str | None = Field(default=None, alias='albumName')
    duration: float | None = None
    instrumental: bool | None = None
    synced_lyrics: str | None = Field(default=None, alias='syncedLyrics')
