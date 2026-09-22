from __future__ import annotations

from pathlib import Path
from unittest.mock import Mock

import pytest

import music_ingest.workers.media_stage as media_stage
from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState
from music_ingest.adapters.inspectors.media_capabilities import MediaCapability
from music_ingest.adapters.remux import RemuxRequest
from music_ingest.services.normalize.metadata import MetadataWriteResult
from music_ingest.workers.media_stage import (
    MediaPipelineInfrastructureError,
    MediaPipelineRequest,
    MediaStagePlan,
    PipelineOutputFailure,
    process_media,
)


def _evidence(state: ToolState, stderr: str = '') -> ToolEvidence:
    return ToolEvidence(state, 1 if state is ToolState.FAILED else None, '', stderr)


@pytest.mark.parametrize('state', [ToolState.MISSING, ToolState.EXECUTION_FAILED, ToolState.TIMED_OUT])
def test_final_output_validation_when_staged_decoder_is_unavailable_does_not_diagnose_source(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state: ToolState
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staged = tmp_path / 'staged.mka'
    staged.write_bytes(b'staged output')
    decoder = Mock(return_value=_evidence(state))
    monkeypatch.setattr(media_stage, 'decoder_evidence', decoder)

    with pytest.raises(MediaPipelineInfrastructureError, match=f'final staged decoder unavailable: {state}'):
        media_stage._validate_final_output(source, staged, 'ffmpeg', 0.25)

    decoder.assert_called_once_with(staged, ffmpeg_command='ffmpeg', timeout_seconds=0.25)
    assert source.read_bytes() == b'immutable source'


def test_final_output_validation_when_only_staged_output_fails_reports_pipeline_failure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staged = tmp_path / 'staged.mka'
    staged.write_bytes(b'invalid staged output')
    staged_evidence = _evidence(ToolState.FAILED, 'staged decode failed')
    source_evidence = _evidence(ToolState.SUCCESS)
    monkeypatch.setattr(media_stage, 'decoder_evidence', Mock(side_effect=(staged_evidence, source_evidence)))

    with pytest.raises(PipelineOutputFailure) as raised:
        media_stage._validate_final_output(source, staged, 'ffmpeg', 1.0)

    assert raised.value.source_path == source
    assert raised.value.staged_path == staged
    assert raised.value.source_evidence is source_evidence
    assert raised.value.staged_evidence is staged_evidence
    assert source.read_bytes() == b'immutable source'


@pytest.mark.parametrize('state', [ToolState.MISSING, ToolState.EXECUTION_FAILED, ToolState.TIMED_OUT])
def test_final_output_validation_when_source_diagnosis_is_unavailable_reports_infrastructure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state: ToolState
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staged = tmp_path / 'staged.mka'
    staged.write_bytes(b'invalid staged output')
    monkeypatch.setattr(
        media_stage,
        'decoder_evidence',
        Mock(side_effect=(_evidence(ToolState.FAILED), _evidence(state))),
    )

    with pytest.raises(
        MediaPipelineInfrastructureError,
        match=f'source decoder unavailable after staged failure: {state}',
    ):
        media_stage._validate_final_output(source, staged, 'ffmpeg', 1.0)

    assert source.read_bytes() == b'immutable source'


def test_process_media_when_fingerprint_is_disabled_never_invokes_fpcalc(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staging = tmp_path / 'staging'
    staging.mkdir()
    plan = MediaStagePlan(source, (), None, 'Unsorted', 'track.mka')
    fingerprint = Mock(side_effect=AssertionError('fpcalc must remain optional'))

    def remux(request: RemuxRequest) -> ToolEvidence:
        request.output_path.write_bytes(b'remuxed output')
        return _evidence(ToolState.SUCCESS)

    def write_observed(path: Path, tags: tuple[tuple[str, str], ...]) -> MetadataWriteResult:
        return MetadataWriteResult(path, tags)

    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)
    monkeypatch.setattr(media_stage, 'write_observed_metadata', write_observed)
    monkeypatch.setattr(media_stage, 'decoder_evidence', lambda *_args, **_kwargs: _evidence(ToolState.SUCCESS))

    result = process_media(
        MediaPipelineRequest(plan, MediaCapability('mp3', 'mp3'), staging, plan.output_name, run_fingerprint=False)
    )

    fingerprint.assert_not_called()
    assert result.fingerprint is None
    assert result.output_path.read_bytes() == b'remuxed output'
    assert source.read_bytes() == b'immutable source'


@pytest.mark.parametrize('staging_kind', ['missing', 'file'])
def test_process_media_rejects_invalid_staging_root_before_touching_source_or_running_tools(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, staging_kind: str
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staging = tmp_path / 'staging'
    if staging_kind == 'file':
        staging.write_bytes(b'not a directory')
    plan = MediaStagePlan(source, (), None, 'Unsorted', 'track.mka')
    remux = Mock()
    fingerprint = Mock()
    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)

    expected = FileNotFoundError if staging_kind == 'missing' else ValueError
    with pytest.raises(expected):
        process_media(MediaPipelineRequest(plan, MediaCapability('mp3', 'mp3'), staging, plan.output_name))

    remux.assert_not_called()
    fingerprint.assert_not_called()
    assert source.read_bytes() == b'immutable source'


@pytest.mark.parametrize('output_kind', ['absolute', 'parent', 'nested'])
def test_process_media_rejects_non_filename_output_before_mutation_or_tools(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, output_kind: str
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staging = tmp_path / 'staging'
    staging.mkdir()
    plan = MediaStagePlan(source, (), None, 'Unsorted', 'track.mka')
    remux = Mock()
    fingerprint = Mock()
    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)
    output_names = {
        'absolute': str(tmp_path / 'outside.mka'),
        'parent': '../outside.mka',
        'nested': 'nested/outside.mka',
    }
    outside = tmp_path / 'outside.mka' if output_kind != 'nested' else staging / 'nested' / 'outside.mka'
    outside.parent.mkdir(exist_ok=True)
    outside.write_bytes(b'existing outside file')

    with pytest.raises(ValueError, match='output name must be a single filename'):
        process_media(MediaPipelineRequest(plan, MediaCapability('mp3', 'mp3'), staging, output_names[output_kind]))

    remux.assert_not_called()
    fingerprint.assert_not_called()
    assert source.read_bytes() == b'immutable source'
    assert outside.read_bytes() == b'existing outside file'


@pytest.mark.parametrize('output_name', ['', '.', '..'])
def test_process_media_rejects_empty_or_dot_output_name_before_running_tools(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, output_name: str
) -> None:
    source = tmp_path / 'incoming.mp3'
    source.write_bytes(b'immutable source')
    staging = tmp_path / 'staging'
    staging.mkdir()
    plan = MediaStagePlan(source, (), None, 'Unsorted', 'track.mka')
    remux = Mock()
    fingerprint = Mock()
    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)

    with pytest.raises(ValueError, match='output name must be a single filename'):
        process_media(MediaPipelineRequest(plan, MediaCapability('mp3', 'mp3'), staging, output_name))

    remux.assert_not_called()
    fingerprint.assert_not_called()
    assert source.read_bytes() == b'immutable source'
    assert tuple(staging.iterdir()) == ()


@pytest.mark.parametrize(
    ('source_name', 'output_name'),
    [
        ('source.mka', 'source.mka'),
        ('.remux.mka', 'track.mka'),
        ('source.mp3', '.remux.mka'),
        ('source.mp3', '.remux.flac'),
    ],
)
def test_process_media_rejects_source_output_or_remux_path_collisions_before_unlink(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    source_name: str,
    output_name: str,
) -> None:
    staging = tmp_path / 'staging'
    staging.mkdir()
    source = staging / source_name
    source.write_bytes(b'immutable source')
    plan = MediaStagePlan(source, (), None, 'Unsorted', 'track.mka')
    remux = Mock()
    fingerprint = Mock()
    monkeypatch.setattr(media_stage, 'remux_stream_copy', remux)
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', fingerprint)

    with pytest.raises(ValueError, match='source, output and remux paths must be distinct'):
        process_media(MediaPipelineRequest(plan, MediaCapability('mp3', 'mp3'), staging, output_name))

    remux.assert_not_called()
    fingerprint.assert_not_called()
    assert source.read_bytes() == b'immutable source'
    assert tuple(staging.iterdir()) == (source,)
