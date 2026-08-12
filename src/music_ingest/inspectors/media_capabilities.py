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


_DECLARED_CAPABILITIES: Final[frozenset[MediaCapability]] = frozenset(
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
    source_path: Path, *, ffprobe_command: str = 'ffprobe', timeout_seconds: float = 10.0
) -> MediaCapabilityInspection:
    ffprobe = run_tool(
        (
            ffprobe_command,
            '-v',
            'error',
            '-show_entries',
            'format=format_name:stream=codec_name',
            '-of',
            'json',
            str(source_path),
        ),
        timeout_seconds,
    )
    if ffprobe.state is not ToolState.SUCCESS:
        return MediaCapabilityInspection(None, ffprobe)
    capability = _parse_capability(ffprobe.stdout)
    return MediaCapabilityInspection(capability, ffprobe)


def _parse_capability(payload: str) -> MediaCapability | None:
    parsed_value: object = json.loads(payload)
    if not _is_json_mapping(parsed_value):
        return None
    format_value = parsed_value.get('format')
    streams_value = parsed_value.get('streams')
    if not _is_json_mapping(format_value) or not isinstance(streams_value, list):
        return None
    format_names = format_value.get('format_name')
    codec_names = tuple(
        stream.get('codec_name')
        for stream in streams_value
        if _is_json_mapping(stream) and isinstance(stream.get('codec_name'), str)
    )
    if not isinstance(format_names, str):
        return None
    for capability in _DECLARED_CAPABILITIES:
        if capability.container == format_names and capability.codec in codec_names:
            return capability
    return None
