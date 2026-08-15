from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import override

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


@dataclass(frozen=True, slots=True)
class RemuxRequest:
    source_path: Path
    output_path: Path
    ffmpeg_command: str = 'ffmpeg'
    timeout_seconds: float = 10.0


@dataclass(frozen=True, slots=True)
class RemuxFailure(Exception):
    source_path: Path
    output_path: Path
    evidence: ToolEvidence

    @override
    def __str__(self) -> str:
        return f'ffmpeg stream-copy remux failed: {self.source_path} ({self.evidence.state})'


def remux_stream_copy(request: RemuxRequest) -> ToolEvidence:
    source_path = request.source_path.resolve(strict=True)
    output_path = request.output_path.resolve()
    muxer = _muxer(source_path.suffix)
    evidence = run_tool(
        (
            request.ffmpeg_command,
            '-nostdin',
            '-hide_banner',
            '-v',
            'error',
            '-xerror',
            '-i',
            str(source_path),
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
            str(output_path),
        ),
        request.timeout_seconds,
    )
    if evidence.state is not ToolState.SUCCESS or not output_path.is_file():
        output_path.unlink(missing_ok=True)
        raise RemuxFailure(source_path, output_path, evidence)
    return evidence


def _muxer(suffix: str) -> str:
    match suffix.casefold():
        case '.flac':
            return 'flac'
        case '.m4a' | '.mp4':
            return 'ipod'
        case '.mp3':
            return 'mp3'
        case '.ogg':
            return 'ogg'
        case '.opus':
            return 'opus'
        case _:
            raise ValueError(f'unsupported stream-copy remux suffix: {suffix}')
