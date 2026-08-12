from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import override

from music_ingest.inspectors._tool import ToolState, run_tool


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
    command = (ffmpeg_command, '-v', 'error', '-xerror', '-i', str(input_path), '-map', '0:a:0', '-f', 'null', '-')
    if runner is not None:
        runner(command, timeout_seconds)
        return
    evidence = run_tool(command, timeout_seconds)
    if evidence.state is not ToolState.SUCCESS:
        raise DecoderValidationError(input_path)
