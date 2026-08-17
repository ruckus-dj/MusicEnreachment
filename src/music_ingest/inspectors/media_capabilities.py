from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Final, TypeGuard

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


@dataclass(frozen=True, slots=True)
class MediaCapability:
    container: str
    codec: str


@dataclass(frozen=True, slots=True)
class MediaCapabilityInspection:
    capability: MediaCapability | None
    ffprobe: ToolEvidence
    audio_stream_count: int = 0


_DECLARED_PUBLICATION_CAPABILITIES: Final[frozenset[MediaCapability]] = frozenset(
    {
        MediaCapability('flac', 'flac'),
        MediaCapability('mov,mp4,m4a,3gp,3g2,mj2', 'alac'),
        MediaCapability('mov,mp4,m4a,3gp,3g2,mj2', 'aac'),
        MediaCapability('mp3', 'mp3'),
        MediaCapability('ogg', 'opus'),
        MediaCapability('ogg', 'vorbis'),
    }
)


def _is_json_mapping(value: object) -> TypeGuard[dict[str, object]]:
    return isinstance(value, dict)


def inspect_media_capability(
    source_path: Path,
    *,
    ffprobe_command: str = 'ffprobe',
    timeout_seconds: float = 10.0,
    publication_only: bool = False,
) -> MediaCapabilityInspection:
    ffprobe = run_tool(
        (
            ffprobe_command,
            '-v',
            'error',
            '-show_entries',
            'format=format_name:stream=codec_type,codec_name',
            '-of',
            'json',
            str(source_path),
        ),
        timeout_seconds,
    )
    if ffprobe.state is not ToolState.SUCCESS:
        return MediaCapabilityInspection(None, ffprobe, 0)
    capability, audio_stream_count = _parse_capability(ffprobe.stdout, publication_only)
    return MediaCapabilityInspection(capability, ffprobe, audio_stream_count)


def _parse_capability(payload: str, publication_only: bool) -> tuple[MediaCapability | None, int]:
    parsed_value: object = json.loads(payload)
    if not _is_json_mapping(parsed_value):
        return None, 0
    format_value = parsed_value.get('format')
    streams_value = parsed_value.get('streams')
    if not _is_json_mapping(format_value) or not isinstance(streams_value, list):
        return None, 0
    format_names = format_value.get('format_name')
    audio_streams = tuple(
        stream
        for stream in streams_value
        if _is_json_mapping(stream)
        and isinstance(stream.get('codec_name'), str)
        and stream.get('codec_type') == 'audio'
    )
    if not isinstance(format_names, str) or len(audio_streams) != 1:
        return None, len(audio_streams)
    codec_name = audio_streams[0].get('codec_name')
    if not isinstance(codec_name, str):
        return None, len(audio_streams)
    capability = MediaCapability(format_names, codec_name)
    if publication_only and capability not in _DECLARED_PUBLICATION_CAPABILITIES:
        return None, len(audio_streams)
    return capability, len(audio_streams)
