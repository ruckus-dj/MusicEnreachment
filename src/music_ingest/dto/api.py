from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class MatchingSettings(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(ge=0.0, le=1.0)


class RuntimeSettingsRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(ge=0.0, le=1.0)
    timeout_seconds: float = Field(gt=0.0, le=120.0)
    retry_delay_seconds: float = Field(ge=0.0, le=3600.0)
    max_attempts: int = Field(ge=1, le=10)
    musicbrainz_enabled: bool
    musicbrainz_user_agent: str = Field(min_length=1, max_length=255)
    acoustid_enabled: bool
    acoustid_client_key: str | None = Field(default=None, max_length=255)
    artwork_enabled: bool


class RuntimeSettingsResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float
    timeout_seconds: float
    retry_delay_seconds: float
    max_attempts: int
    musicbrainz_enabled: bool
    musicbrainz_user_agent: str
    acoustid_enabled: bool
    acoustid_client_key_configured: bool
    artwork_enabled: bool


class GenreCatalogItemResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_id: str
    source_name: str
    display_name: str


class GenreCatalogResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[GenreCatalogItemResponse, ...]
    last_synced_at: datetime | None


class LibraryIdentityUpdate(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_recording_id: str = Field(
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$'
    )


class MetadataUpdate(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    tags: dict[str, str]


class ProviderRetryResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    queued: int


class ProviderRetryRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    retry_all: bool = False
    provider: Literal['acoustid', 'musicbrainz'] | None = None


class ProviderRetryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    queued: bool


class CandidateSelection(BaseModel):
    model_config = ConfigDict(frozen=True)

    candidate_key: str = Field(min_length=1, max_length=255)
    provider: Literal['acoustid', 'musicbrainz'] = 'musicbrainz'


class MusicBrainzOverride(BaseModel):
    model_config = ConfigDict(frozen=True)

    release_mbid: str = Field(min_length=1, max_length=36)


class CandidateEvidencePayload(BaseModel):
    model_config = ConfigDict(frozen=True)

    provider: str = 'musicbrainz'
    artist: str = ''
    release: str = ''
    title: str = ''
    album: str = ''
    score: float | None = None
    tags: dict[str, str] = Field(default_factory=dict)


class RecoveryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    queued: int
    skipped: int
    conflicts: int


class SourceRecoveryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    record_id: str
    source_id: str
    queued: bool
    kind: str | None


class DestinationConflictCleanupResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    record_id: str
    source_id: str
    path: str
    removed: bool
    queued: bool


class LidarrDispatchResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    job_id: str | None
    replayed: bool


class Genre(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    name: str


class ArtistCredit(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    name: str
    genres: tuple[Genre, ...] = ()


class ReleaseGroup(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    first_release_date: str | None = Field(default=None, alias='first-release-date')


class TrackRecording(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    id: str
    title: str
    artist_credit: tuple[ArtistCredit, ...] = ()
    genres: tuple[Genre, ...] = ()


class Track(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    position: int
    title: str
    length: int | None = None
    recording: TrackRecording


class Medium(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    position: int
    track_count: int | None = Field(default=None, alias='track-count')
    tracks: tuple[Track, ...] = ()


class Release(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    title: str
    status: str | None = None
    artist_credit: tuple[ArtistCredit, ...] = ()
    date: str | None = None
    genres: tuple[Genre, ...] = ()
    release_group: ReleaseGroup | None = Field(default=None, alias='release-group')
    media: tuple[Medium, ...] = ()


class ReleaseResponse(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[Release, ...]


class RecordingResponse(ReleaseResponse):
    pass


class EvidenceFixturePayload(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    outcome: str
    release_mbid: str | None = None
    release_title: str | None = None
    artist_name: str | None = None
    recording_mbid: str | None = None
    score: float | None = None
