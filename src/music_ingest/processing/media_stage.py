from __future__ import annotations

import shutil
from dataclasses import dataclass
from pathlib import Path

from music_ingest.dto.settings import RuntimeSettings
from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.decoder import DecoderValidationError, decoder_evidence, validate_decoder
from music_ingest.inspectors.flac import FlacInspectionResult, inspect_flac
from music_ingest.inspectors.media_capabilities import MediaCapability, inspect_media_capability
from music_ingest.inspectors.mp3 import Mp3InspectionResult, inspect_mp3
from music_ingest.normalize.metadata import (
    CanonicalMetadata,
    MetadataWriteRequest,
    MetadataWriteResult,
    write_canonical_metadata,
    write_observed_metadata,
)
from music_ingest.processing.metadata import (
    fallback_metadata,
    field_policy,
    genre_policy,
    publication_layout,
    read_tags,
)
from music_ingest.sanitizers.flac import FlacSanitizationRequest, FlacStreamIdentity, sanitize_flac


@dataclass(frozen=True, slots=True)
class SourceMediaInspection:
    capability: MediaCapability | None
    flac: FlacInspectionResult | None
    mp3: Mp3InspectionResult | None
    decoder: ToolEvidence | None


@dataclass(frozen=True, slots=True)
class MediaStagePlan:
    source_path: Path
    source_tags: tuple[tuple[str, str], ...]
    metadata: CanonicalMetadata | None
    relative_directory: str
    output_name: str


@dataclass(frozen=True, slots=True)
class MediaStageRequest:
    plan: MediaStagePlan
    staging_directory: Path
    output_name: str
    ffmpeg_command: str = 'ffmpeg'
    timeout_seconds: float = 10.0
    runtime_settings: RuntimeSettings | None = None


@dataclass(frozen=True, slots=True)
class MediaStageResult:
    output_path: Path
    written_tags: tuple[tuple[str, str], ...]
    flac_streaminfo: FlacStreamIdentity | None


def inspect_source_capability(source_path: Path, *, timeout_seconds: float = 10.0) -> MediaCapability | None:
    return inspect_media_capability(source_path, timeout_seconds=timeout_seconds).capability


def inspect_source_media(
    source_path: Path,
    *,
    ffmpeg_command: str = 'ffmpeg',
    timeout_seconds: float = 10.0,
    cached_decoder_evidence: ToolEvidence | None = None,
    capability: MediaCapability | None = None,
) -> SourceMediaInspection:
    if capability is None:
        capability = inspect_source_capability(source_path, timeout_seconds=timeout_seconds)
    if capability is None:
        return SourceMediaInspection(None, None, None, None)
    suffix = source_path.suffix.casefold()
    if suffix == '.flac':
        inspection = inspect_flac(
            source_path,
            ffmpeg_command=ffmpeg_command,
            timeout_seconds=timeout_seconds,
            cached_decoder_evidence=cached_decoder_evidence,
        )
        return SourceMediaInspection(capability, inspection, None, None)
    if suffix == '.mp3':
        return SourceMediaInspection(capability, None, inspect_mp3(source_path, timeout_seconds=timeout_seconds), None)
    evidence = cached_decoder_evidence or decoder_evidence(
        source_path, ffmpeg_command=ffmpeg_command, timeout_seconds=timeout_seconds
    )
    if evidence.state is not ToolState.SUCCESS:
        raise DecoderValidationError(source_path)
    return SourceMediaInspection(capability, None, None, evidence)


def plan_media_stage(source_path: Path) -> MediaStagePlan:
    source_tags = read_tags(source_path)
    metadata = fallback_metadata(source_tags)
    relative_directory, output_name = publication_layout(source_tags, source_path.name)
    return MediaStagePlan(source_path, source_tags, metadata, relative_directory, output_name)


def stage_media(request: MediaStageRequest) -> MediaStageResult:
    staging_directory = request.staging_directory.resolve(strict=True)
    if not staging_directory.is_dir():
        raise ValueError('staging directory must be a directory')
    source_path = request.plan.source_path.resolve(strict=True)
    output_path = staging_directory / request.output_name
    if output_path.exists():
        raise FileExistsError(output_path)
    is_flac = source_path.suffix.casefold() == '.flac'
    sanitized_path = staging_directory / ('.sanitized.flac' if is_flac else f'.staged{source_path.suffix}')
    flac_streaminfo: FlacStreamIdentity | None = None
    if is_flac:
        sanitized = sanitize_flac(
            FlacSanitizationRequest(
                source_path,
                sanitized_path,
                staging_directory,
                request.ffmpeg_command,
                request.timeout_seconds,
            )
        )
        flac_streaminfo = sanitized.streaminfo
    else:
        _ = shutil.copy2(source_path, sanitized_path)

    metadata_result: MetadataWriteResult
    if request.plan.metadata is None:
        metadata_result = write_observed_metadata(sanitized_path, request.plan.source_tags)
        _ = sanitized_path.rename(output_path)
        metadata_result = MetadataWriteResult(output_path, metadata_result.tags)
    else:
        metadata_result = write_canonical_metadata(
            MetadataWriteRequest(
                sanitized_path,
                output_path,
                staging_directory,
                request.plan.metadata,
                field_policy(),
                genre_policy(request.plan.metadata.genres, request.runtime_settings),
            )
        )
        sanitized_path.unlink()
    stage_source_artwork(source_path, staging_directory)
    validate_decoder(output_path, ffmpeg_command=request.ffmpeg_command, timeout_seconds=request.timeout_seconds)
    return MediaStageResult(output_path, metadata_result.tags, flac_streaminfo)


def stage_source_artwork(source_path: Path, staging_directory: Path) -> None:
    artwork = next(
        (path for path in (source_path.parent / 'cover.jpg', source_path.parent / 'cover.webp') if path.is_file()),
        None,
    )
    if artwork is not None:
        _ = shutil.copy2(artwork, staging_directory / artwork.name)
