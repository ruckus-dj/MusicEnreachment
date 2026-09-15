from pydantic import BaseModel, ConfigDict, Field


class WorkerPoolSettings(BaseModel):
    model_config = ConfigDict(frozen=True, extra='forbid')

    filesystem_scan: int = Field(default=4, ge=1, le=8)
    acoustid_analysis: int = Field(default=1, ge=1, le=8)
    musicbrainz_analysis: int = Field(default=4, ge=1, le=8)
    candidate_selection: int = Field(default=4, ge=1, le=8)
    folder_release_selection: int = Field(default=2, ge=1, le=8)
    final_publish: int = Field(default=4, ge=1, le=8)
    selection_refresh: int = Field(default=4, ge=1, le=8)
    lrclib_fetch: int = Field(default=1, ge=1, le=8)
    artwork_enrichment: int = Field(default=2, ge=1, le=8)
    reconciliation_scan: int = Field(default=1, ge=1, le=8)


class RuntimeSettings(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(default=0.7, ge=0.0, le=1.0)
    timeout_seconds: float = Field(default=10.0, gt=0.0, le=120.0)
    retry_delay_seconds: float = Field(default=30.0, ge=0.0, le=3600.0)
    max_attempts: int = Field(default=3, ge=1, le=10)
    worker_pools: WorkerPoolSettings = Field(default_factory=WorkerPoolSettings)
    musicbrainz_enabled: bool = True
    musicbrainz_user_agent: str = Field(
        default='music-ingest/0.1.0 (music-ingest@example.com)', min_length=1, max_length=255
    )
    musicbrainz_host: str = Field(default='https://musicbrainz.org', pattern=r'^https?://[^/?#]+$')
    musicbrainz_request_delay_seconds: float = Field(default=1.5, ge=0.0, le=3600.0)
    acoustid_enabled: bool = False
    acoustid_request_delay_seconds: float = Field(default=1 / 3, gt=0.0, le=3600.0)
    acoustid_client_key: str | None = Field(default=None, max_length=255)
    artwork_enabled: bool = True
    lrclib_enabled: bool = True
    lrclib_host: str = Field(default='https://lrclib.net', pattern=r'^https://[^/?#]+$')
    lrclib_user_agent: str = Field(
        default='music-ingest/0.1.0 (music-ingest@example.com)', min_length=1, max_length=255
    )
    lrclib_timeout_seconds: float = Field(default=15.0, gt=0.0, le=120.0)
    lrclib_max_attempts: int = Field(default=3, ge=1, le=10)
    lrclib_request_delay_seconds: float = Field(default=0.3, ge=0.0, le=3600.0)
    lrclib_max_response_bytes: int = Field(default=4 * 1024 * 1024, ge=1024, le=16 * 1024 * 1024)
    lrclib_match_confidence_threshold: float = Field(default=0.7, ge=0.0, le=1.0)
    canonical_genres: tuple[str, ...] = ()
    genre_aliases: dict[str, tuple[str, ...]] = {}
