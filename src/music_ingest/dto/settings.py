from pydantic import BaseModel, ConfigDict, Field


class RuntimeSettings(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(default=0.7, ge=0.0, le=1.0)
    timeout_seconds: float = Field(default=10.0, gt=0.0, le=120.0)
    retry_delay_seconds: float = Field(default=30.0, ge=0.0, le=3600.0)
    max_attempts: int = Field(default=3, ge=1, le=10)
    musicbrainz_enabled: bool = True
    musicbrainz_user_agent: str = Field(
        default='music-ingest/0.1.0 (music-ingest@example.com)', min_length=1, max_length=255
    )
    acoustid_enabled: bool = False
    acoustid_client_key: str | None = Field(default=None, max_length=255)
    artwork_enabled: bool = True
    canonical_genres: tuple[str, ...] = ()
    genre_aliases: dict[str, tuple[str, ...]] = {}
