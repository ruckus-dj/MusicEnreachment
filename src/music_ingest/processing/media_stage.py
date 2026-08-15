from __future__ import annotations

import shutil
from concurrent.futures import Future, ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import override

from mutagen.flac import FLAC

from music_ingest.dto.settings import RuntimeSettings
from music_ingest.enrichment.fingerprints import FingerprintResult, calculate_fingerprint
from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.decoder import decoder_evidence
from music_ingest.inspectors.media_capabilities import MediaCapability, inspect_media_capability
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
from music_ingest.processing.remux import RemuxRequest, remux_stream_copy


@dataclass(frozen=True, slots=True)
class MediaStagePlan:
    source_path: Path
    source_tags: tuple[tuple[str, str], ...]
    metadata: CanonicalMetadata | None
    relative_directory: str
    output_name: str


@dataclass(frozen=True, slots=True)
class MediaPipelineRequest:
    plan: MediaStagePlan
    capability: MediaCapability
    staging_directory: Path
    output_name: str
    ffmpeg_command: str = 'ffmpeg'
    fpcalc_command: str = 'fpcalc'
    timeout_seconds: float = 10.0
    runtime_settings: RuntimeSettings | None = None
    run_fingerprint: bool = True


@dataclass(frozen=True, slots=True)
class MediaPipelineResult:
    output_path: Path
    written_tags: tuple[tuple[str, str], ...]
    fingerprint: FingerprintResult | None
    flac_properties: FlacProperties | None


@dataclass(frozen=True, slots=True)
class FlacProperties:
    bit_depth: int
    sample_rate: int
    channels: int


@dataclass(frozen=True, slots=True)
class MediaPipelineInfrastructureError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


@dataclass(frozen=True, slots=True)
class SourceAudioCorruptionError(Exception):
    source_path: Path
    staged_path: Path
    source_evidence: ToolEvidence
    staged_evidence: ToolEvidence

    @override
    def __str__(self) -> str:
        return f'source decoder rejected audio: {self.source_path}'


@dataclass(frozen=True, slots=True)
class PipelineOutputFailure(Exception):
    source_path: Path
    staged_path: Path
    source_evidence: ToolEvidence
    staged_evidence: ToolEvidence

    @override
    def __str__(self) -> str:
        return f'staged decoder rejected remuxed media while source passed: {self.staged_path}'


def inspect_source_capability(source_path: Path, *, timeout_seconds: float = 10.0) -> MediaCapability | None:
    return inspect_media_capability(source_path, timeout_seconds=timeout_seconds).capability


def plan_media_stage(source_path: Path) -> MediaStagePlan:
    source_tags = read_tags(source_path)
    metadata = fallback_metadata(source_tags)
    relative_directory, output_name = publication_layout(source_tags, source_path.name)
    return MediaStagePlan(source_path, source_tags, metadata, relative_directory, output_name)


def process_media(request: MediaPipelineRequest) -> MediaPipelineResult:
    staging_directory = request.staging_directory.resolve(strict=True)
    if not staging_directory.is_dir():
        raise ValueError('staging directory must be a directory')
    source_path = request.plan.source_path.resolve(strict=True)
    output_path = staging_directory / request.output_name
    remux_path = staging_directory / f'.remux{source_path.suffix}'
    _ = output_path.unlink(missing_ok=True)
    _ = remux_path.unlink(missing_ok=True)

    with ThreadPoolExecutor(max_workers=2, thread_name_prefix='media-tool') as executor:
        remux_future = executor.submit(
            remux_stream_copy,
            RemuxRequest(source_path, remux_path, request.ffmpeg_command, request.timeout_seconds),
        )
        fingerprint_future: Future[FingerprintResult] | None = None
        if request.run_fingerprint:
            fingerprint_future = executor.submit(
                calculate_fingerprint,
                source_path,
                None,
                fpcalc_command=request.fpcalc_command,
                timeout_seconds=request.timeout_seconds,
            )
        remux_error: BaseException | None = None
        try:
            _ = remux_future.result()
        except BaseException as error:
            remux_error = error
        fingerprint = None if fingerprint_future is None else fingerprint_future.result()
        if remux_error is not None:
            raise remux_error

    metadata_result = _write_staged_metadata(request, remux_path, output_path)
    stage_source_artwork(source_path, staging_directory)
    _validate_final_output(source_path, output_path, request.ffmpeg_command, request.timeout_seconds)
    _ = remux_path.unlink(missing_ok=True)
    flac_properties = _flac_properties(output_path) if source_path.suffix.casefold() == '.flac' else None
    return MediaPipelineResult(output_path, metadata_result.tags, fingerprint, flac_properties)


def _write_staged_metadata(
    request: MediaPipelineRequest,
    remux_path: Path,
    output_path: Path,
) -> MetadataWriteResult:
    if request.plan.metadata is None:
        observed = write_observed_metadata(remux_path, request.plan.source_tags)
        remux_path.rename(output_path)
        return MetadataWriteResult(output_path, observed.tags)
    result = write_canonical_metadata(
        MetadataWriteRequest(
            remux_path,
            output_path,
            request.staging_directory,
            request.plan.metadata,
            field_policy(),
            genre_policy(request.plan.metadata.genres, request.runtime_settings),
            request.capability,
        )
    )
    remux_path.unlink()
    return result


def _validate_final_output(source_path: Path, output_path: Path, ffmpeg_command: str, timeout_seconds: float) -> None:
    staged_evidence = decoder_evidence(output_path, ffmpeg_command=ffmpeg_command, timeout_seconds=timeout_seconds)
    if staged_evidence.state is ToolState.SUCCESS:
        return
    if staged_evidence.state is not ToolState.FAILED:
        raise MediaPipelineInfrastructureError(f'final staged decoder unavailable: {staged_evidence.state}')
    source_evidence = decoder_evidence(source_path, ffmpeg_command=ffmpeg_command, timeout_seconds=timeout_seconds)
    if source_evidence.state is ToolState.FAILED:
        raise SourceAudioCorruptionError(source_path, output_path, source_evidence, staged_evidence)
    if source_evidence.state is ToolState.SUCCESS:
        raise PipelineOutputFailure(source_path, output_path, source_evidence, staged_evidence)
    raise MediaPipelineInfrastructureError(f'source decoder unavailable after staged failure: {source_evidence.state}')


def _flac_properties(path: Path) -> FlacProperties:
    info = FLAC(path).info
    return FlacProperties(info.bits_per_sample, info.sample_rate, info.channels)


def stage_source_artwork(source_path: Path, staging_directory: Path) -> None:
    artwork = next(
        (path for path in (source_path.parent / 'cover.jpg', source_path.parent / 'cover.webp') if path.is_file()),
        None,
    )
    if artwork is not None:
        _ = shutil.copy2(artwork, staging_directory / artwork.name)
