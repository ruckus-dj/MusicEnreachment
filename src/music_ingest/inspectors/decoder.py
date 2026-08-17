from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import override

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


@dataclass(frozen=True, slots=True)
class DecoderValidationError(Exception):
    path: Path
    evidence: ToolEvidence | None = None

    @override
    def __str__(self) -> str:
        if self.evidence is None:
            return f'ffmpeg decoder rejected audio stream: {self.path}'
        detail = self.evidence.stderr.strip() or self.evidence.stdout.strip() or 'no tool output'
        return f'ffmpeg decoder rejected audio stream ({self.evidence.state}): {self.path}; {detail}'


def validate_decoder(
    input_path: Path,
    *,
    ffmpeg_command: str = 'ffmpeg',
    timeout_seconds: float = 10.0,
    runner: Callable[[tuple[str, ...], float], None] | None = None,
) -> ToolEvidence | None:
    if runner is not None:
        runner(_command(input_path, ffmpeg_command), timeout_seconds)
        return None
    evidence = decoder_evidence(input_path, ffmpeg_command=ffmpeg_command, timeout_seconds=timeout_seconds)
    if evidence.state is not ToolState.SUCCESS:
        raise DecoderValidationError(input_path, evidence)
    return evidence


def decoder_evidence(
    input_path: Path, *, ffmpeg_command: str = 'ffmpeg', timeout_seconds: float = 10.0
) -> ToolEvidence:
    return run_tool(_command(input_path, ffmpeg_command), timeout_seconds)


def _command(input_path: Path, ffmpeg_command: str) -> tuple[str, ...]:
    return (ffmpeg_command, '-v', 'error', '-i', str(input_path), '-map', '0:a:0', '-f', 'null', '-')
