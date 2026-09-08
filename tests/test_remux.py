from __future__ import annotations

from pathlib import Path
from unittest.mock import Mock

import pytest

from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.processing import remux


@pytest.mark.parametrize(
    'state', [ToolState.FAILED, ToolState.MISSING, ToolState.EXECUTION_FAILED, ToolState.TIMED_OUT]
)
@pytest.mark.parametrize('partial_output', [False, True])
def test_remux_failure_preserves_evidence_and_source_and_removes_partial_output(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state: ToolState, partial_output: bool
) -> None:
    source = tmp_path / 'source.flac'
    source.write_bytes(b'original')
    output = tmp_path / 'output.mka'
    sibling = tmp_path / 'keep.nfo'
    sibling.write_bytes(b'keep')
    evidence = ToolEvidence(state, 1 if state == ToolState.FAILED else None, 'stdout', 'failure')

    def run_tool(command: tuple[str, ...], timeout: float) -> ToolEvidence:
        assert command[-1] == str(output)
        assert timeout == 0.5
        if partial_output:
            output.write_bytes(b'partial')
        return evidence

    monkeypatch.setattr(remux, 'run_tool', run_tool)
    with pytest.raises(remux.RemuxFailure) as raised:
        remux.remux_stream_copy(remux.RemuxRequest(source, output, timeout_seconds=0.5))
    assert raised.value.evidence is evidence
    assert raised.value.source_path == source
    assert raised.value.output_path == output
    assert str(state) in str(raised.value)
    assert not output.exists()
    assert source.read_bytes() == b'original'
    assert sibling.read_bytes() == b'keep'


def test_remux_rejects_success_without_output(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    source = tmp_path / 'source.flac'
    source.write_bytes(b'original')
    output = tmp_path / 'output.mka'
    evidence = ToolEvidence(ToolState.SUCCESS, 0, '', '')
    monkeypatch.setattr(remux, 'run_tool', Mock(return_value=evidence))
    with pytest.raises(remux.RemuxFailure) as raised:
        remux.remux_stream_copy(remux.RemuxRequest(source, output))
    assert raised.value.evidence is evidence
    assert not output.exists()
    assert source.read_bytes() == b'original'


@pytest.mark.parametrize(
    ('suffix', 'muxer'),
    [
        ('.mka', 'matroska'),
        ('.FLAC', 'flac'),
        ('.mp4', 'ipod'),
        ('.m4a', 'ipod'),
        ('.mp3', 'mp3'),
        ('.ogg', 'ogg'),
        ('.opus', 'opus'),
    ],
)
def test_remux_success_uses_audio_only_copy_and_non_overwriting_command(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, suffix: str, muxer: str
) -> None:
    source = tmp_path / 'source.flac'
    source.write_bytes(b'original')
    output = tmp_path / f'output{suffix}'
    evidence = ToolEvidence(ToolState.SUCCESS, 0, 'stdout', '')

    def run_tool(command: tuple[str, ...], timeout: float) -> ToolEvidence:
        assert command == (
            'custom-ffmpeg',
            '-nostdin',
            '-hide_banner',
            '-v',
            'error',
            '-xerror',
            '-i',
            str(source),
            '-map',
            '0:a:0',
            '-map_metadata',
            '-1',
            '-map_chapters',
            '-1',
            '-c:a',
            'copy',
            '-f',
            muxer,
            '-n',
            str(output),
        )
        assert timeout == 2.5
        output.write_bytes(b'derived')
        return evidence

    monkeypatch.setattr(remux, 'run_tool', run_tool)
    assert remux.remux_stream_copy(remux.RemuxRequest(source, output, 'custom-ffmpeg', 2.5)) is evidence
    assert source.read_bytes() == b'original'
    assert output.read_bytes() == b'derived'


@pytest.mark.parametrize('invalid', ['missing-source', 'unsupported-suffix'])
def test_remux_invalid_request_does_not_run_ffmpeg(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, invalid: str
) -> None:
    source = tmp_path / 'source.flac'
    if invalid != 'missing-source':
        source.write_bytes(b'original')
    output = tmp_path / ('output.wav' if invalid == 'unsupported-suffix' else 'output.mka')
    run_tool = Mock()
    monkeypatch.setattr(remux, 'run_tool', run_tool)
    expected = FileNotFoundError if invalid == 'missing-source' else ValueError
    with pytest.raises(expected):
        remux.remux_stream_copy(remux.RemuxRequest(source, output))
    run_tool.assert_not_called()
    assert not output.exists()


@pytest.mark.parametrize('existing', ['file', 'source', 'hardlink', 'symlink', 'dangling-symlink', 'directory'])
def test_remux_rejects_existing_output_without_deleting_it(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, existing: str
) -> None:
    source = tmp_path / 'source.flac'
    source.write_bytes(b'original')
    output = tmp_path / 'output.mka'
    if existing == 'source':
        output = source
    elif existing == 'hardlink':
        output.hardlink_to(source)
    elif existing == 'symlink':
        output.symlink_to(source)
    elif existing == 'dangling-symlink':
        output.symlink_to(tmp_path / 'missing.mka')
    elif existing == 'directory':
        output.mkdir()
    else:
        output.write_bytes(b'existing')
    original_stat = output.lstat()
    run_tool = Mock(return_value=ToolEvidence(ToolState.FAILED, 1, '', 'already exists'))
    monkeypatch.setattr(remux, 'run_tool', run_tool)
    with pytest.raises(FileExistsError):
        remux.remux_stream_copy(remux.RemuxRequest(source, output))
    run_tool.assert_not_called()
    assert output.lstat() == original_stat
    assert source.read_bytes() == b'original'
    if existing == 'file':
        assert output.read_bytes() == b'existing'
