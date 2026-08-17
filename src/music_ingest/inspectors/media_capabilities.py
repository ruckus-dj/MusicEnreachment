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
class MediaTechnicalProperties:
    bit_depth: int | None = None
    sample_rate: int | None = None
    channels: int | None = None
    bitrate: int | None = None


@dataclass(frozen=True, slots=True)
class MediaCapabilityInspection:
    capability: MediaCapability | None
    ffprobe: ToolEvidence
    audio_stream_count: int = 0
    technical: MediaTechnicalProperties | None = None


_DECLARED_PUBLICATION_CAPABILITIES: Final[frozenset[MediaCapability]] = frozenset(
    {
        MediaCapability('flac', 'flac'),
        MediaCapability('mov,mp4,m4a,3gp,3g2,mj2', 'alac'),
        MediaCapability('mov,mp4,m4a,3gp,3g2,mj2', 'aac'),
        MediaCapability('mp3', 'mp3'),
        MediaCapability('ogg', 'opus'),
        MediaCapability('ogg', 'vorbis'),
        MediaCapability('matroska,webm', 'aac'),
        MediaCapability('matroska,webm', 'alac'),
        MediaCapability('matroska,webm', 'flac'),
        MediaCapability('matroska,webm', 'mp3'),
        MediaCapability('matroska,webm', 'opus'),
        MediaCapability('matroska,webm', 'vorbis'),
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
            'format=format_name:stream=codec_type,codec_name,bits_per_sample,bits_per_raw_sample,sample_rate,channels,bit_rate',
            '-of',
            'json',
            str(source_path),
        ),
        timeout_seconds,
    )
    if ffprobe.state is not ToolState.SUCCESS:
        return MediaCapabilityInspection(None, ffprobe, 0)
    capability, audio_stream_count, technical = _parse_capability(ffprobe.stdout, publication_only)
    return MediaCapabilityInspection(capability, ffprobe, audio_stream_count, technical)


def _parse_capability(
    payload: str, publication_only: bool
) -> tuple[MediaCapability | None, int, MediaTechnicalProperties | None]:
    parsed_value: object = json.loads(payload)
    if not _is_json_mapping(parsed_value):
        return None, 0, None
    format_value = parsed_value.get('format')
    streams_value = parsed_value.get('streams')
    if not _is_json_mapping(format_value) or not isinstance(streams_value, list):
        return None, 0, None
    format_names = format_value.get('format_name')
    audio_streams = tuple(
        stream
        for stream in (value for value in streams_value if _is_json_mapping(value))
        if isinstance(stream.get('codec_name'), str) and stream.get('codec_type') == 'audio'
    )
    if not isinstance(format_names, str) or len(audio_streams) != 1:
        return None, len(audio_streams), None
    codec_name = audio_streams[0].get('codec_name')
    if not isinstance(codec_name, str):
        return None, len(audio_streams), None
    capability = MediaCapability(format_names, codec_name)
    if publication_only and capability not in _DECLARED_PUBLICATION_CAPABILITIES:
        return None, len(audio_streams), None
    stream = audio_streams[0]
    technical = MediaTechnicalProperties(
        bit_depth=_positive_int(stream.get('bits_per_sample')) or _positive_int(stream.get('bits_per_raw_sample')),
        sample_rate=_positive_int(stream.get('sample_rate')),
        channels=_positive_int(stream.get('channels')),
        bitrate=_positive_int(stream.get('bit_rate')),
    )
    return capability, len(audio_streams), technical


def _positive_int(value: object) -> int | None:
    if isinstance(value, int) and value > 0:
        return value
    if not isinstance(value, str):
        return None
    try:
        parsed = int(value)
    except ValueError:
        return None
    return parsed if parsed > 0 else None
