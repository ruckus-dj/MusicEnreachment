from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from tempfile import TemporaryDirectory
from typing import override

from music_ingest.enrichment.fingerprints import calculate_fingerprint
from music_ingest.inspectors._tool import ToolState
from music_ingest.inspectors.decoder import DecoderValidationError
from music_ingest.inspectors.flac import FlacFindingKind
from music_ingest.inspectors.flac import InspectionState as FlacInspectionState
from music_ingest.inspectors.mp3 import InspectionState as Mp3InspectionState
from music_ingest.normalize.metadata import MetadataWriteError
from music_ingest.processing.media_stage import (
    MediaStageRequest,
    inspect_source_media,
    plan_media_stage,
    stage_media,
)
from music_ingest.sanitizers.flac import FlacSanitizationFailure


@dataclass(frozen=True, slots=True)
class MediaStageCliError(Exception):
    description: str

    @override
    def __str__(self) -> str:
        return self.description


@dataclass(frozen=True, slots=True)
class MediaStageCliResult:
    output_path: Path


def run_media_stage_cli(
    input_path: Path,
    output_directory: Path,
    temporary_directory: Path,
    *,
    ffmpeg_command: str = 'ffmpeg',
    fpcalc_command: str = 'fpcalc',
    timeout_seconds: float = 10.0,
) -> MediaStageCliResult:
    source = input_path.resolve(strict=True)
    if not source.is_file():
        raise MediaStageCliError('input must be a regular file')
    output_root = output_directory.resolve()
    temporary_root = temporary_directory.resolve()
    if output_root.is_relative_to(source.parent) or temporary_root.is_relative_to(source.parent):
        raise MediaStageCliError('output and temporary directories must be outside the source directory')
    output_root.mkdir(parents=True, exist_ok=True)
    temporary_root.mkdir(parents=True, exist_ok=True)
    if output_root.stat().st_dev != temporary_root.stat().st_dev:
        raise MediaStageCliError('output and temporary directories must be on the same filesystem')

    try:
        inspection = inspect_source_media(
            source,
            ffmpeg_command=ffmpeg_command,
            timeout_seconds=timeout_seconds,
        )
    except (DecoderValidationError, CalledProcessError, OSError, TimeoutExpired) as error:
        raise MediaStageCliError(str(error)) from error
    if inspection.capability is None:
        raise MediaStageCliError('source has no declared media capability')
    if inspection.flac is not None:
        if inspection.flac.state is FlacInspectionState.QUARANTINE:
            raise MediaStageCliError('malformed FLAC container')
        has_repairable_wrapper = any(
            finding.kind is FlacFindingKind.TRAILING_ID3V1 for finding in inspection.flac.findings
        )
        if inspection.flac.flac_test.state is ToolState.FAILED and not has_repairable_wrapper:
            raise MediaStageCliError('FLAC decoder rejected audio')
        if inspection.flac.flac_test.state is not ToolState.SUCCESS and not has_repairable_wrapper:
            raise MediaStageCliError(f'FLAC decoder unavailable: {inspection.flac.flac_test.state}')
    if inspection.mp3 is not None:
        if inspection.mp3.state is Mp3InspectionState.QUARANTINE:
            raise MediaStageCliError('malformed MP3 container')
        if inspection.mp3.state is Mp3InspectionState.INFRASTRUCTURE:
            raise MediaStageCliError(f'MP3 inspection unavailable: {inspection.mp3.ffprobe.state}')
    fingerprint_inspection = inspection.flac or inspection.mp3
    if fingerprint_inspection is not None:
        _ = calculate_fingerprint(
            source,
            fingerprint_inspection,
            fpcalc_command=fpcalc_command,
            timeout_seconds=timeout_seconds,
        )

    plan = plan_media_stage(source)
    destination = output_root / plan.relative_directory / plan.output_name
    if destination.exists():
        raise MediaStageCliError(f'output already exists: {destination}')
    with TemporaryDirectory(dir=temporary_root, prefix='media-stage-') as temporary_name:
        staging_directory = Path(temporary_name)
        try:
            result = stage_media(
                MediaStageRequest(
                    plan,
                    staging_directory,
                    plan.output_name,
                    ffmpeg_command,
                    timeout_seconds,
                )
            )
        except (
            DecoderValidationError,
            FlacSanitizationFailure,
            MetadataWriteError,
            CalledProcessError,
            OSError,
            TimeoutExpired,
        ) as error:
            raise MediaStageCliError(str(error)) from error
        destination.parent.mkdir(parents=True, exist_ok=True)
        os.replace(result.output_path, destination)
    return MediaStageCliResult(destination)
