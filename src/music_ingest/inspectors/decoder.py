from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import override

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


@dataclass(frozen=True, slots=True)
class DecoderValidationError(Exception):
    path: Path

    @override
    def __str__(self) -> str:
        return f'ffmpeg decoder rejected staged media: {self.path}'


def validate_decoder(
    input_path: Path,
    *,
    ffmpeg_command: str = 'ffmpeg',
    timeout_seconds: float = 10.0,
    runner: Callable[[tuple[str, ...], float], None] | None = None,
) -> None:
    if runner is not None:
        runner(_command(input_path, ffmpeg_command), timeout_seconds)
        return
    evidence = decoder_evidence(input_path, ffmpeg_command=ffmpeg_command, timeout_seconds=timeout_seconds)
    if evidence.state is not ToolState.SUCCESS:
        raise DecoderValidationError(input_path)


def decoder_evidence(
    input_path: Path, *, ffmpeg_command: str = 'ffmpeg', timeout_seconds: float = 10.0
) -> ToolEvidence:
    return run_tool(_command(input_path, ffmpeg_command), timeout_seconds)


def _command(input_path: Path, ffmpeg_command: str) -> tuple[str, ...]:
    return (ffmpeg_command, '-v', 'error', '-xerror', '-i', str(input_path), '-map', '0:a:0', '-f', 'null', '-')
