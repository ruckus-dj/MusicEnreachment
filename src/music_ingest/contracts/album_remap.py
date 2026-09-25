from __future__ import annotations

from typing import final
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, model_validator


@final
class AlbumRemapSelector(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True, str_strip_whitespace=True)

    release_mbid: UUID | None = None
    artist_name: str | None = Field(default=None, min_length=1, max_length=500)
    album_name: str | None = Field(default=None, min_length=1, max_length=500)
    artist_missing: bool = False
    album_missing: bool = False

    @model_validator(mode='after')
    def _has_one_selector_shape(self) -> AlbumRemapSelector:
        if self.artist_name is not None and self.artist_missing:
            raise ValueError('artist name and missing flag are mutually exclusive')
        if sum((self.release_mbid is not None, self.album_name is not None, self.album_missing)) != 1:
            raise ValueError('exactly one album selector is required')
        return self


@final
class AlbumRemapContextRequest(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    selector: AlbumRemapSelector


@final
class AlbumRemapSource(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    source_id: str
    record_id: str
    path: str
    sha256: str
    duration_seconds: int | None
    source_metadata_revision: int
    recording_mbid: str | None
    release_mbid: str | None
    canonical_tags: dict[str, str]


@final
class AlbumRemapContextResponse(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    album_snapshot_token: str
    sources: tuple[AlbumRemapSource, ...]


@final
class AlbumReleaseSearchRequest(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True, str_strip_whitespace=True)

    query: str = Field(min_length=1, max_length=500)


@final
class AlbumReleaseSummary(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    release_mbid: str
    title: str
    artist_credit: str
    date: str | None = None
    country: str | None = None


@final
class AlbumReleaseSearchResponse(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    releases: tuple[AlbumReleaseSummary, ...]


@final
class AlbumRemapPreviewRequest(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    selector: AlbumRemapSelector
    album_snapshot_token: str = Field(pattern=r'^[0-9a-f]{64}$')
    release_mbid: UUID


@final
class AlbumRemapTrackSlot(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    track_mbid: str
    recording_mbid: str
    medium_position: int
    track_position: int
    title: str
    artist_credit: str
    duration_seconds: float | None
    assignable: bool
    suggested_source_id: str | None = None


@final
class AlbumRemapPreviewResponse(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    release_snapshot_token: str
    release: AlbumReleaseSummary
    track_slots: tuple[AlbumRemapTrackSlot, ...]


@final
class AlbumRemapAssignment(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    source_id: str = Field(min_length=1, max_length=64)
    track_mbid: UUID


@final
class AlbumRemapApplyRequest(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    selector: AlbumRemapSelector
    album_snapshot_token: str = Field(pattern=r'^[0-9a-f]{64}$')
    release_mbid: UUID
    release_snapshot_token: str = Field(pattern=r'^[0-9a-f]{64}$')
    assignments: tuple[AlbumRemapAssignment, ...]
    unmatched_source_ids: tuple[str, ...]


@final
class AlbumRemapApplyResponse(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    assigned_source_ids: tuple[str, ...]
    unmatched_source_ids: tuple[str, ...]
    publication_refresh_queued: bool
    queued_release_artwork: bool
