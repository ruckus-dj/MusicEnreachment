from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


class InspectionState(StrEnum):
    VALID = 'valid'
    QUARANTINE = 'quarantine'


class Mp3FindingKind(StrEnum):
    ID3V2 = 'id3v2'
    MP3_FRAME = 'mp3_frame'
    TRAILING_ID3V1 = 'trailing_id3v1'
    MALFORMED_CONTAINER = 'malformed_container'
    FFPROBE_FAILED = 'ffprobe_failed'
    FFPROBE_UNAVAILABLE = 'ffprobe_unavailable'
    FFPROBE_TIMED_OUT = 'ffprobe_timed_out'


@dataclass(frozen=True, slots=True)
class Mp3Finding:
    kind: Mp3FindingKind
    offset: int | None
    size: int | None


@dataclass(frozen=True, slots=True)
class Mp3InspectionResult:
    state: InspectionState
    findings: tuple[Mp3Finding, ...]
    id3v2_version: int | None
    id3v2_size: int | None
    ffprobe: ToolEvidence


def inspect_mp3(
    source_path: Path, *, ffprobe_command: str = 'ffprobe', timeout_seconds: float = 10.0
) -> Mp3InspectionResult:
    payload = source_path.read_bytes()
    findings, version, tag_size, malformed = _parse_mp3(payload)
    ffprobe = run_tool(
        (
            ffprobe_command,
            '-v',
            'error',
            '-show_entries',
            'format=format_name',
            '-of',
            'default=nw=1:nk=1',
            str(source_path),
        ),
        timeout_seconds,
    )
    findings += _tool_findings(ffprobe)
    state = InspectionState.QUARANTINE if malformed or ffprobe.state is not ToolState.SUCCESS else InspectionState.VALID
    return Mp3InspectionResult(state, findings, version, tag_size, ffprobe)


def _parse_mp3(payload: bytes) -> tuple[tuple[Mp3Finding, ...], int | None, int | None, bool]:
    offset, version, tag_size, id3_finding, malformed = _leading_id3v2(payload)
    findings = (id3_finding,) if id3_finding is not None else ()
    if malformed or not _is_mp3_frame(payload, offset):
        return findings + (Mp3Finding(Mp3FindingKind.MALFORMED_CONTAINER, offset, None),), version, tag_size, True
    findings += (Mp3Finding(Mp3FindingKind.MP3_FRAME, offset, 4),)
    if len(payload) >= 128 and payload[-128:-125] == b'TAG':
        findings += (Mp3Finding(Mp3FindingKind.TRAILING_ID3V1, len(payload) - 128, 128),)
    return findings, version, tag_size, False


def _leading_id3v2(payload: bytes) -> tuple[int, int | None, int | None, Mp3Finding | None, bool]:
    if not payload.startswith(b'ID3'):
        return 0, None, None, None, False
    if len(payload) < 10 or payload[3] not in (2, 3, 4) or any(value & 0x80 for value in payload[6:10]):
        return 0, None, None, None, True
    tag_size = sum(value << (7 * index) for index, value in enumerate(reversed(payload[6:10])))
    total_size = 10 + tag_size + (10 if payload[3] == 4 and payload[5] & 0x10 else 0)
    if total_size > len(payload):
        return 0, payload[3], tag_size, None, True
    return total_size, payload[3], tag_size, Mp3Finding(Mp3FindingKind.ID3V2, 0, total_size), False


def _is_mp3_frame(payload: bytes, offset: int) -> bool:
    if len(payload) - offset < 4:
        return False
    header = int.from_bytes(payload[offset : offset + 4], 'big')
    sync = header >> 21
    version = (header >> 19) & 0b11
    layer = (header >> 17) & 0b11
    bitrate = (header >> 12) & 0b1111
    sample_rate = (header >> 10) & 0b11
    return (
        sync == 0b11111111111 and version != 0b01 and layer != 0 and bitrate not in (0, 0b1111) and sample_rate != 0b11
    )


def _tool_findings(tool_evidence: ToolEvidence) -> tuple[Mp3Finding, ...]:
    match tool_evidence.state:
        case ToolState.SUCCESS:
            return ()
        case ToolState.FAILED:
            return (Mp3Finding(Mp3FindingKind.FFPROBE_FAILED, None, None),)
        case ToolState.MISSING:
            return (Mp3Finding(Mp3FindingKind.FFPROBE_UNAVAILABLE, None, None),)
        case ToolState.EXECUTION_FAILED:
            return (Mp3Finding(Mp3FindingKind.FFPROBE_FAILED, None, None),)
        case ToolState.TIMED_OUT:
            return (Mp3Finding(Mp3FindingKind.FFPROBE_TIMED_OUT, None, None),)
