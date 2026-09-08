from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path

from music_ingest.dto import FieldPolicy, GenrePolicy
from music_ingest.enrichment.artwork import (
    ArtworkProvider,
)
from music_ingest.matching.providers import (
    AcoustIdProvider,
    LiveTransport,
    MusicBrainzProvider,
)
from music_ingest.matching.scoring import (
    DEFAULT_CONFIDENCE_THRESHOLD,
)


@dataclass(frozen=True, slots=True)
class ProcessingConfig:
    incoming_root: Path
    staging_root: Path
    media_root: Path
    ffmpeg_command: str = 'ffmpeg'
    fpcalc_command: str = 'fpcalc'
    timeout_seconds: float = 10.0
    retry_delay: timedelta = timedelta(seconds=30)
    max_attempts: int = 3
    field_policy: FieldPolicy | None = None
    genre_policy: GenrePolicy | None = None
    live_transport: LiveTransport | None = None
    musicbrainz_provider: MusicBrainzProvider | None = None
    acoustid_provider: AcoustIdProvider | None = None
    artwork_provider: ArtworkProvider | None = None
    confidence_threshold: float = DEFAULT_CONFIDENCE_THRESHOLD
    unsorted_filename_allocator: Callable[[str], str] | None = None
