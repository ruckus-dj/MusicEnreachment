from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from tempfile import TemporaryDirectory
from typing import override

from music_ingest.adapters.remux import RemuxFailure
from music_ingest.services.normalize.metadata import MetadataWriteError
from music_ingest.workers.media_stage import (
    MediaPipelineInfrastructureError,
    MediaPipelineRequest,
    PipelineOutputFailure,
    SourceAudioCorruptionError,
    inspect_source_capability,
    plan_media_stage,
    process_media,
)


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

    capability = inspect_source_capability(source, timeout_seconds=timeout_seconds)
    if capability is None:
        raise MediaStageCliError('source has no declared media capability')
    plan = plan_media_stage(source)
    destination = output_root / plan.relative_directory / plan.output_name
    if destination.exists():
        raise MediaStageCliError(f'output already exists: {destination}')
    with TemporaryDirectory(dir=temporary_root, prefix='media-stage-') as temporary_name:
        staging_directory = Path(temporary_name)
        try:
            result = process_media(
                MediaPipelineRequest(
                    plan,
                    capability,
                    staging_directory,
                    plan.output_name,
                    ffmpeg_command,
                    fpcalc_command,
                    timeout_seconds,
                )
            )
        except (
            MediaPipelineInfrastructureError,
            PipelineOutputFailure,
            RemuxFailure,
            SourceAudioCorruptionError,
            MetadataWriteError,
            CalledProcessError,
            OSError,
            TimeoutExpired,
        ) as error:
            raise MediaStageCliError(str(error)) from error
        destination.parent.mkdir(parents=True, exist_ok=True)
        os.replace(result.output_path, destination)
    return MediaStageCliResult(destination)
