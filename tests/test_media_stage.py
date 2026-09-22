from __future__ import annotations

from hashlib import sha256
from pathlib import Path
from shutil import which
from subprocess import run
from threading import Barrier

import pytest

import music_ingest.workers.media_stage as media_stage
from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState
from music_ingest.adapters.remux import RemuxRequest, _muxer
from music_ingest.cli.media_stage import run_media_stage_cli
from music_ingest.services.enrichment.fingerprints import FingerprintResult, FingerprintState
from music_ingest.workers.media_stage import (
    MediaPipelineRequest,
    SourceAudioCorruptionError,
    inspect_source_capability,
    plan_media_stage,
    process_media,
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
    plan = plan_media_stage(source)

    result = process_media(MediaPipelineRequest(plan, capability, staging, plan.output_name, run_fingerprint=False))

    assert result.output_path.is_file()
    assert result.output_path.suffix == '.mka'
    assert sha256(source.read_bytes()).hexdigest() == original_hash
    assert not list(staging.glob('.staged*'))
    if suffix == '.flac':
        assert result.flac_properties is not None
    else:
        assert result.flac_properties is None


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


@pytest.mark.parametrize(
    ('suffix', 'muxer'),
    [('.mka', 'matroska'), ('.flac', 'flac'), ('.m4a', 'ipod'), ('.mp3', 'mp3'), ('.ogg', 'ogg'), ('.opus', 'opus')],
)
def test_stream_copy_remux_when_declared_format_uses_native_muxer(suffix: str, muxer: str) -> None:
    assert _muxer(suffix) == muxer


def test_process_media_when_remux_and_fingerprint_run_can_overlap(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = _audio(tmp_path / 'source.mp3', 'libmp3lame')
    staging = tmp_path / 'staging'
    staging.mkdir()
    capability = inspect_source_capability(source)
    assert capability is not None
    plan = plan_media_stage(source)
    barrier = Barrier(2)
    entered: list[str] = []

    def remux(request: RemuxRequest) -> ToolEvidence:
        entered.append('remux')
        barrier.wait(timeout=2)
        request.output_path.write_bytes(request.source_path.read_bytes())
        return ToolEvidence(ToolState.SUCCESS, 0, '', '')

    def fingerprint(*_args: object, **_kwargs: object) -> FingerprintResult:
        entered.append('fingerprint')
        barrier.wait(timeout=2)
        return FingerprintResult(FingerprintState.SUCCESS, 'fixture', 1, 'fixture', 'a' * 64, None, None)

    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)
    monkeypatch.setattr(
        media_stage,
        'decoder_evidence',
        lambda *_args, **_kwargs: ToolEvidence(ToolState.SUCCESS, 0, '', ''),
    )

    _ = process_media(MediaPipelineRequest(plan, capability, staging, plan.output_name))

    assert sorted(entered) == ['fingerprint', 'remux']


def test_process_media_when_remuxed_output_and_source_fail_reports_source_corruption(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = _audio(tmp_path / 'source.mp3', 'libmp3lame')
    staging = tmp_path / 'staging'
    staging.mkdir()
    capability = inspect_source_capability(source)
    assert capability is not None
    plan = plan_media_stage(source)

    def remux(request: RemuxRequest) -> ToolEvidence:
        request.output_path.write_bytes(request.source_path.read_bytes())
        return ToolEvidence(ToolState.SUCCESS, 0, '', '')

    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(
        media_stage,
        'decoder_evidence',
        lambda *_args, **_kwargs: ToolEvidence(ToolState.FAILED, 1, '', 'decode failed'),
    )

    with pytest.raises(SourceAudioCorruptionError):
        process_media(MediaPipelineRequest(plan, capability, staging, plan.output_name, run_fingerprint=False))
