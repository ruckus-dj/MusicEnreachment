from __future__ import annotations

from hashlib import sha256
from pathlib import Path
from shutil import which
from subprocess import run

import pytest

from music_ingest.cli.media_stage import run_media_stage_cli
from music_ingest.processing.media_stage import (
    MediaStageRequest,
    inspect_source_capability,
    inspect_source_media,
    plan_media_stage,
    stage_media,
)

_FFMPEG = which('ffmpeg') or ''
assert _FFMPEG


def _audio(path: Path, codec: str) -> Path:
    completed = run(  # noqa: S603
        [
            _FFMPEG,
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-c:a',
            codec,
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return path


@pytest.mark.parametrize(('suffix', 'codec'), [('.mp3', 'libmp3lame'), ('.flac', 'flac')])
def test_shared_stage_when_worker_processes_audio_writes_valid_output_without_mutating_source(
    tmp_path: Path, suffix: str, codec: str
) -> None:
    source = _audio(tmp_path / f'source{suffix}', codec)
    original_hash = sha256(source.read_bytes()).hexdigest()
    staging = tmp_path / 'staging'
    staging.mkdir()
    capability = inspect_source_capability(source)
    assert capability is not None
    inspection = inspect_source_media(source, capability=capability)
    plan = plan_media_stage(source)

    result = stage_media(MediaStageRequest(plan, staging, plan.output_name))

    assert result.output_path.is_file()
    assert result.output_path.suffix == suffix
    assert sha256(source.read_bytes()).hexdigest() == original_hash
    assert inspection.capability == capability
    assert not list(staging.glob('.staged*'))
    if suffix == '.flac':
        assert result.flac_streaminfo is not None
    else:
        assert result.flac_streaminfo is None


def test_cli_when_processing_real_service_stage_writes_only_derived_audio(tmp_path: Path) -> None:
    source_directory = tmp_path / 'source'
    source_directory.mkdir()
    source = _audio(source_directory / 'source.flac', 'flac')
    output_directory = tmp_path / 'output'
    temporary_directory = tmp_path / 'tmp'

    result = run_media_stage_cli(source, output_directory, temporary_directory)

    assert result.output_path.is_file()
    assert result.output_path.parent == output_directory / 'Unsorted'
    assert tuple(path for path in output_directory.rglob('*') if path.is_file()) == (result.output_path,)
    assert tuple(temporary_directory.iterdir()) == ()
