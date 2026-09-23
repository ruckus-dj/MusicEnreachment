from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from music_ingest.contracts.settings import WorkerPoolSettings


class MatchingSettings(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(ge=0.0, le=1.0)


class RuntimeSettingsRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(ge=0.0, le=1.0)
    timeout_seconds: float = Field(gt=0.0, le=120.0)
    retry_delay_seconds: float = Field(ge=0.0, le=3600.0)
    max_attempts: int = Field(ge=1, le=10)
    worker_pools: WorkerPoolSettings
    musicbrainz_enabled: bool
    musicbrainz_user_agent: str = Field(min_length=1, max_length=255)
    musicbrainz_host: str = Field(pattern=r'^https?://[^/?#]+$')
    musicbrainz_request_delay_seconds: float = Field(ge=0.0, le=3600.0)
    acoustid_enabled: bool
    acoustid_request_delay_seconds: float = Field(gt=0.0, le=3600.0)
    acoustid_client_key: str | None = Field(default=None, max_length=255)
    artwork_enabled: bool
    lrclib_enabled: bool
    lrclib_host: str = Field(pattern=r'^https://[^/?#]+$')
    lrclib_user_agent: str = Field(min_length=1, max_length=255)
    lrclib_timeout_seconds: float = Field(gt=0.0, le=120.0)
    lrclib_max_attempts: int = Field(ge=1, le=10)
    lrclib_request_delay_seconds: float = Field(ge=0.0, le=3600.0)
    lrclib_max_response_bytes: int = Field(ge=1024, le=16 * 1024 * 1024)
    lrclib_match_confidence_threshold: float = Field(ge=0.0, le=1.0)


class RuntimeSettingsResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float
    timeout_seconds: float
    retry_delay_seconds: float
    max_attempts: int
    worker_pools: WorkerPoolSettings
    musicbrainz_enabled: bool
    musicbrainz_user_agent: str
    musicbrainz_host: str
    musicbrainz_request_delay_seconds: float
    acoustid_enabled: bool
    acoustid_request_delay_seconds: float
    acoustid_client_key_configured: bool
    artwork_enabled: bool
    lrclib_enabled: bool
    lrclib_host: str
    lrclib_user_agent: str
    lrclib_timeout_seconds: float
    lrclib_max_attempts: int
    lrclib_request_delay_seconds: float
    lrclib_max_response_bytes: int
    lrclib_match_confidence_threshold: float


class SourceRootCreateRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    path: str = Field(min_length=1, max_length=4096)
    display_name: str = Field(min_length=1, max_length=255)


class SourceRootUpdateRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    display_name: str = Field(min_length=1, max_length=255)
    enabled: bool


class SourceRootResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    display_name: str
    canonical_path: str
    enabled: bool
    scan_state: str


class SourceRootListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[SourceRootResponse, ...]


class SourceRootCandidateResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str
    canonical_path: str


class SourceRootCandidateListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[SourceRootCandidateResponse, ...]


class StoragePathRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    path: str = Field(min_length=1, max_length=4096)


class StorageBrowserItemResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str
    path: str


class StorageBrowserResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    path: str
    parent_path: str | None
    items: tuple[StorageBrowserItemResponse, ...]


class StorageOutputPreviewResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    output_root: str
    same_filesystem: bool
    file_count: int


class NextUnsortedFilenameResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    filename: str


class StorageConfigResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    output_root: str
    state: str
    generation: int


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
    musicbrainz_release_id: str = Field(
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$'
    )


class LibraryPublicationQuery(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    published: bool | None = None


class LibraryCatalogQuery(LibraryPublicationQuery):
    artist: str | None = Field(default=None, min_length=1, max_length=1024)
    artist_missing: bool = False

    @model_validator(mode='after')
    def require_artist_selector(self) -> LibraryCatalogQuery:
        if self.artist is not None and self.artist_missing:
            raise ValueError('artist and artist_missing=true cannot be combined')
        return self


class LibraryTrackQuery(LibraryCatalogQuery):
    album_id: str | None = Field(default=None, min_length=1, max_length=255)
    album_name: str | None = Field(default=None, min_length=1, max_length=1024)
    album_missing: bool = False

    @model_validator(mode='after')
    def require_one_album_selector(self) -> LibraryTrackQuery:
        selector_count = sum(value is not None for value in (self.album_id, self.album_name)) + self.album_missing
        if selector_count > 1 or (selector_count == 0 and (self.artist is not None or self.artist_missing)):
            raise ValueError('exactly one of album_id, album_name, or album_missing=true is required')
        return self


class LibraryArtistResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    name: str | None
    track_count: int = Field(ge=0)


class LibraryArtistListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[LibraryArtistResponse, ...]
    total_track_count: int = Field(ge=0)


class LibraryAlbumResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    album_id: str | None
    album_name: str | None
    track_count: int = Field(ge=0)
    artwork_url: str | None = None


class LibraryAlbumListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[LibraryAlbumResponse, ...]


LyricsStatus = Literal['none', 'pending', 'synced', 'no_candidate', 'validation_rejected', 'error']


class LibraryTrackResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    record_id: str
    source_id: str
    source_path: str
    artist_name: str | None
    album_name: str | None
    album_id: str | None
    title: str
    track_number: str | None
    source_state: str
    processing_state: str
    match_state: str
    publication_state: str
    lyrics_status: LyricsStatus
    lyrics_synced: bool


class LibraryTrackListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[LibraryTrackResponse, ...]


class LibraryRecordSourceResponse(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    source_id: str
    path: str
    format: str
    sha256: str
    state: str
    tag_observations: tuple[dict[str, str], ...] = ()
    disappeared_at: str | None = None


class LibraryRecordSummaryResponse(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    record_id: str
    musicbrainz_recording_id: str | None = None
    musicbrainz_release_id: str | None = None
    musicbrainz_artist_id: str | None = None
    artwork: dict[str, str] | None = None
    source_state: str
    processing_state: str
    match_state: str
    publication_state: str
    metadata_state: str
    metadata_revisions: tuple[dict[str, object], ...] = ()
    sources: tuple[LibraryRecordSourceResponse, ...]
    publications: tuple[dict[str, object], ...] = ()


class LibraryRecordListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[LibraryRecordSummaryResponse, ...]


ManualActionFilter = Literal['analysis-error', 'needs-review']


class ManualActionCountsResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    analysis_error: int
    needs_review: int


class ManualActionListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    items: tuple[LibraryRecordSummaryResponse, ...]
    counts: ManualActionCountsResponse


class MetadataUpdate(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    tags: dict[str, str]


class ProviderRetryResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    queued: int


class FullReprocessResponse(BaseModel):
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
    entity: Literal['recording', 'recording_release'] = 'recording_release'


class ManualSourceSelection(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str = Field(min_length=1, max_length=64)


class MusicBrainzOverride(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    recording_mbid: str = Field(
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$'
    )


class MusicBrainzReleaseLookup(BaseModel):
    model_config = ConfigDict(frozen=True)

    recording_mbid: str | None = Field(
        default=None,
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$',
    )
    release_mbid: str | None = Field(
        default=None,
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$',
    )


class CandidateReleasePayload(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    release_mbid: str
    artist: str = ''
    title: str = ''
    album: str = ''
    tags: dict[str, str] = Field(default_factory=dict)


class CandidateScoreComponents(BaseModel):
    model_config = ConfigDict(frozen=True)

    artist: float = 0.0
    release: float = 0.0
    duration: float = 0.0
    title: float = 0.0
    track: float = 0.0
    track_number: float | None = None
    track_total: float | None = None
    disc_number: float | None = None
    disc_total: float | None = None
    musicbrainz: float | None = None
    acoustid: float | None = None
    artist_match: float | None = None
    release_match: float | None = None
    duration_match: float | None = None
    title_match: float | None = None
    track_number_match: float | None = None
    track_total_match: float | None = None
    disc_number_match: float | None = None
    disc_total_match: float | None = None
    musicbrainz_match: float | None = None
    acoustid_match: float | None = None
    recording_artist: float | None = None
    release_artist: float | None = None
    recording_artist_match: float | None = None
    release_artist_match: float | None = None


class CandidateEvidencePayload(BaseModel):
    model_config = ConfigDict(frozen=True)

    provider: str = 'musicbrainz'
    entity: Literal['recording', 'recording_release'] = 'recording_release'
    artist: str = ''
    release: str = ''
    title: str = ''
    album: str = ''
    disambiguation: str | None = None
    recording_mbid: str | None = None
    release_mbid: str | None = None
    compatible_ids: tuple[str, ...] = ()
    score: float | None = None
    duration_seconds: int | None = None
    acoustid_score: float | None = None
    musicbrainz_score: float | None = None
    score_components: CandidateScoreComponents | None = None
    tags: dict[str, str] = Field(default_factory=dict)
    releases: tuple[CandidateReleasePayload, ...] = ()


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
    replacement_record_id: str | None = None
    replacement_source_id: str | None = None


class DestinationConflictCleanupResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    record_id: str
    source_id: str
    path: str
    removed: bool
    queued: bool


class Genre(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    name: str


class Artist(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    id: str
    name: str
    genres: tuple[Genre, ...] = ()


class ArtistRelation(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    type: str
    target_type: str = Field(alias='target-type')
    artist: Artist | None = None


class ArtistCredit(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    name: str
    joinphrase: str = ''
    artist: Artist | None = None
    genres: tuple[Genre, ...] = ()


class ReleaseGroup(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    first_release_date: str | None = Field(default=None, alias='first-release-date')


class TrackRecording(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    id: str
    title: str
    artist_credit: tuple[ArtistCredit, ...] = Field(default=(), alias='artist-credit')
    genres: tuple[Genre, ...] = ()
    isrcs: tuple[str, ...] = ()
    relations: tuple[ArtistRelation, ...] = ()


class Track(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    position: int
    title: str
    length: int | None = None
    recording: TrackRecording


class Medium(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    position: int | None = None
    track_count: int | None = Field(default=None, alias='track-count')
    tracks: tuple[Track, ...] = ()
    data_tracks: tuple[Track, ...] = Field(default=(), alias='data-tracks')


class LabelInfo(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    catalog_number: str | None = Field(default=None, alias='catalog-number')


class Release(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True, populate_by_name=True)

    id: str
    title: str
    disambiguation: str | None = None
    status: str | None = None
    artist_credit: tuple[ArtistCredit, ...] = Field(default=(), alias='artist-credit')
    date: str | None = None
    country: str | None = None
    barcode: str | None = None
    label_info: tuple[LabelInfo, ...] = Field(default=(), alias='label-info')
    genres: tuple[Genre, ...] = ()
    release_group: ReleaseGroup | None = Field(default=None, alias='release-group')
    media: tuple[Medium, ...] = ()


class ReleaseResponse(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    releases: tuple[Release, ...]


class ReleaseBrowseResponse(ReleaseResponse):
    count: int = Field(alias='release-count', ge=0)
    offset: int = Field(alias='release-offset', ge=0)


class RecordingResponse(ReleaseResponse):
    pass


class RecordingSearchResult(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    id: str
    score: float | None = None
    title: str = ''
    artist_credit: tuple[ArtistCredit, ...] = Field(default=(), alias='artist-credit')


class RecordingSearchResponse(BaseModel):
    model_config = ConfigDict(extra='ignore', frozen=True)

    recordings: tuple[RecordingSearchResult, ...]


class EvidenceFixturePayload(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)

    outcome: str
    release_mbid: str | None = None
    release_title: str | None = None
    artist_name: str | None = None
    recording_mbid: str | None = None
    score: float | None = None
