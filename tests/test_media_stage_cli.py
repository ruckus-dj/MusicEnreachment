from __future__ import annotations

from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from unittest.mock import Mock

import pytest

from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState
from music_ingest.adapters.inspectors.media_capabilities import MediaCapability
from music_ingest.adapters.remux import RemuxFailure
from music_ingest.cli import media_stage as cli
from music_ingest.services.normalize.metadata import MetadataWriteError
from music_ingest.workers.media_stage import (
    MediaPipelineInfrastructureError,
    MediaPipelineRequest,
    MediaStagePlan,
    PipelineOutputFailure,
    SourceAudioCorruptionError,
)


@pytest.fixture
def source(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    path = incoming / 'track.flac'
    path.write_bytes(b'immutable source')
    monkeypatch.setattr(cli, 'inspect_source_capability', lambda *_args, **_kwargs: MediaCapability('flac', 'flac'))
    monkeypatch.setattr(cli, 'plan_media_stage', lambda path: MediaStagePlan(path, (), None, 'Unsorted', 'track.mka'))
    return path


@pytest.mark.parametrize('input_kind', ['missing', 'directory'])
def test_cli_rejects_invalid_input_before_creating_directories(tmp_path: Path, input_kind: str) -> None:
    source = tmp_path / 'input'
    if input_kind == 'directory':
        source.mkdir()
    expected = FileNotFoundError if input_kind == 'missing' else cli.MediaStageCliError
    with pytest.raises(expected):
        cli.run_media_stage_cli(source, tmp_path / 'output', tmp_path / 'temporary')
    assert not (tmp_path / 'output').exists()
    assert not (tmp_path / 'temporary').exists()


@pytest.mark.parametrize('unsafe_root', ['output', 'temporary'])
@pytest.mark.parametrize('via_symlink', [False, True])
def test_cli_rejects_roots_inside_source_directory(
    source: Path, tmp_path: Path, unsafe_root: str, via_symlink: bool, monkeypatch: pytest.MonkeyPatch
) -> None:
    root = source.parent
    if via_symlink:
        root = tmp_path / 'incoming-link'
        root.symlink_to(source.parent, target_is_directory=True)
    output = root / 'output' if unsafe_root == 'output' else tmp_path / 'output'
    temporary = root / 'temporary' if unsafe_root == 'temporary' else tmp_path / 'temporary'
    process = Mock()
    monkeypatch.setattr(cli, 'process_media', process)
    with pytest.raises(cli.MediaStageCliError, match='outside the source directory'):
        cli.run_media_stage_cli(source, output, temporary)
    process.assert_not_called()
    assert source.read_bytes() == b'immutable source'
    assert list(source.parent.iterdir()) == [source]
    assert not output.exists()
    assert not temporary.exists()


def test_cli_rejects_unsupported_capability_without_processing(
    source: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(cli, 'inspect_source_capability', lambda *_args, **_kwargs: None)
    process = Mock()
    monkeypatch.setattr(cli, 'process_media', process)
    with pytest.raises(cli.MediaStageCliError, match='no declared media capability'):
        cli.run_media_stage_cli(source, tmp_path / 'output', tmp_path / 'temporary')
    process.assert_not_called()
    assert source.read_bytes() == b'immutable source'
    assert list((tmp_path / 'output').iterdir()) == []
    assert list((tmp_path / 'temporary').iterdir()) == []


def test_cli_preserves_existing_destination_without_processing(
    source: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    output = tmp_path / 'output'
    destination = output / 'Unsorted' / 'track.mka'
    destination.parent.mkdir(parents=True)
    destination.write_bytes(b'existing publication')
    process = Mock()
    monkeypatch.setattr(cli, 'process_media', process)
    with pytest.raises(cli.MediaStageCliError, match='output already exists'):
        cli.run_media_stage_cli(source, output, tmp_path / 'temporary')
    process.assert_not_called()
    assert destination.read_bytes() == b'existing publication'
    assert source.read_bytes() == b'immutable source'
    assert list((tmp_path / 'temporary').iterdir()) == []


@pytest.mark.parametrize(
    'failure_kind',
    ['infrastructure', 'output', 'source', 'remux', 'metadata', 'subprocess', 'os', 'timeout'],
)
def test_cli_pipeline_failure_preserves_source_and_cleans_only_its_staging(
    source: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch, failure_kind: str
) -> None:
    output = tmp_path / 'output'
    temporary = tmp_path / 'temporary'
    temporary.mkdir()
    unrelated = temporary / 'another-job'
    unrelated.mkdir()
    marker = unrelated / 'keep.nfo'
    marker.write_bytes(b'keep')
    evidence = ToolEvidence(ToolState.FAILED, 1, '', 'decode error')
    staged = temporary / 'track.mka'
    failures: dict[str, Exception] = {
        'infrastructure': MediaPipelineInfrastructureError('unavailable'),
        'output': PipelineOutputFailure(source, staged, evidence, evidence),
        'source': SourceAudioCorruptionError(source, staged, evidence, evidence),
        'remux': RemuxFailure(source, staged, evidence),
        'metadata': MetadataWriteError('write failed'),
        'subprocess': CalledProcessError(1, 'ffmpeg', stderr='failed'),
        'os': OSError('disk full'),
        'timeout': TimeoutExpired('ffmpeg', 0.25),
    }
    failure = failures[failure_kind]
    staging_directories: list[Path] = []

    def fail(request: MediaPipelineRequest) -> None:
        assert request.plan.source_path == source
        assert request.ffmpeg_command == 'custom-ffmpeg'
        assert request.fpcalc_command == 'custom-fpcalc'
        assert request.timeout_seconds == 0.25
        staging_directories.append(request.staging_directory)
        (request.staging_directory / request.output_name).write_bytes(b'partial output')
        raise failure

    monkeypatch.setattr(cli, 'process_media', fail)
    with pytest.raises(cli.MediaStageCliError) as raised:
        cli.run_media_stage_cli(
            source,
            output,
            temporary,
            ffmpeg_command='custom-ffmpeg',
            fpcalc_command='custom-fpcalc',
            timeout_seconds=0.25,
        )
    assert raised.value.__cause__ is failure
    assert str(raised.value) == str(failure)
    assert source.read_bytes() == b'immutable source'
    assert len(staging_directories) == 1
    assert not staging_directories[0].exists()
    assert list(output.iterdir()) == []
    assert list(temporary.iterdir()) == [unrelated]
    assert marker.read_bytes() == b'keep'
