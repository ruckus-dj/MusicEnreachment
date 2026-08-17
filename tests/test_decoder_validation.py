from pathlib import Path

import pytest

from music_ingest.inspectors.decoder import DecoderValidationError, validate_decoder


def test_validate_decoder_invokes_ffmpeg_for_declared_staged_input(tmp_path: Path) -> None:
    input_path = tmp_path / 'staged.flac'
    _ = input_path.write_bytes(b'fixture')
    calls: list[tuple[str, ...]] = []

    def run(command: tuple[str, ...], timeout_seconds: float) -> None:
        calls.append(command)
        del timeout_seconds

    _ = validate_decoder(input_path, ffmpeg_command='ffmpeg', timeout_seconds=7.0, runner=run)

    assert calls == [('ffmpeg', '-v', 'error', '-i', str(input_path), '-map', '0:a:0', '-f', 'null', '-')]


def test_validate_decoder_accepts_diagnostics_when_ffmpeg_completes(tmp_path: Path) -> None:
    input_path = tmp_path / 'playable-with-diagnostics.mp3'
    _ = input_path.write_bytes(b'fixture')
    ffmpeg = tmp_path / 'ffmpeg-with-diagnostics'
    _ = ffmpeg.write_text('#!/bin/sh\nprintf "%s\\n" "decoder warning" >&2\nexit 0\n')
    ffmpeg.chmod(0o755)

    evidence = validate_decoder(input_path, ffmpeg_command=str(ffmpeg))

    assert evidence is not None
    assert evidence.return_code == 0
    assert evidence.stderr == 'decoder warning\n'


def test_validate_decoder_rejects_failed_container_before_publication(tmp_path: Path) -> None:
    input_path = tmp_path / 'staged.flac'
    _ = input_path.write_bytes(b'malformed')

    def run(_command: tuple[str, ...], _timeout_seconds: float) -> None:
        raise DecoderValidationError(input_path)

    with pytest.raises(DecoderValidationError):
        _ = validate_decoder(input_path, runner=run)

    assert not (tmp_path / 'published.flac').exists()
