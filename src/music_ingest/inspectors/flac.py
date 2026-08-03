from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path

from music_ingest.inspectors._tool import ToolEvidence, ToolState, run_tool


class InspectionState(StrEnum):
    VALID = 'valid'
    QUARANTINE = 'quarantine'


class FlacFindingKind(StrEnum):
    LEADING_ID3V2 = 'leading_id3v2'
    FLAC_MARKER = 'flac_marker'
    STREAMINFO = 'streaminfo'
    VORBIS_COMMENT = 'vorbis_comment'
    PICTURE = 'picture'
    APPLICATION = 'application'
    PADDING = 'padding'
    METADATA_BLOCK = 'metadata_block'
    TRAILING_ID3V1 = 'trailing_id3v1'
    MALFORMED_CONTAINER = 'malformed_container'
    FLAC_TEST_FAILED = 'flac_test_failed'
    FLAC_TEST_UNAVAILABLE = 'flac_test_unavailable'
    FLAC_TEST_TIMED_OUT = 'flac_test_timed_out'


@dataclass(frozen=True, slots=True)
class FlacFinding:
    kind: FlacFindingKind
    offset: int | None
    size: int | None


@dataclass(frozen=True, slots=True)
class FlacInspectionResult:
    state: InspectionState
    findings: tuple[FlacFinding, ...]
    flac_test: ToolEvidence


def inspect_flac(
    source_path: Path, *, flac_command: str = 'flac', timeout_seconds: float = 10.0
) -> FlacInspectionResult:
    payload = source_path.read_bytes()
    findings, malformed = _parse_flac(payload)
    flac_test = run_tool((flac_command, '-t', str(source_path)), timeout_seconds)
    findings = findings + _tool_findings(flac_test)
    has_trailing_id3v1 = any(finding.kind is FlacFindingKind.TRAILING_ID3V1 for finding in findings)
    flac_test_rejects_container = flac_test.state is not ToolState.SUCCESS and not has_trailing_id3v1
    state = InspectionState.QUARANTINE if malformed or flac_test_rejects_container else InspectionState.VALID
    return FlacInspectionResult(state, findings, flac_test)


def _parse_flac(payload: bytes) -> tuple[tuple[FlacFinding, ...], bool]:
    offset, id3_finding, malformed_id3 = _leading_id3v2(payload)
    findings = (id3_finding,) if id3_finding is not None else ()
    if malformed_id3 or payload[offset : offset + 4] != b'fLaC':
        return findings + (FlacFinding(FlacFindingKind.MALFORMED_CONTAINER, offset, None),), True
    findings += (FlacFinding(FlacFindingKind.FLAC_MARKER, offset, 4),)
    offset += 4
    is_last = False
    first_block = True
    while not is_last:
        if len(payload) - offset < 4:
            return findings + (FlacFinding(FlacFindingKind.MALFORMED_CONTAINER, offset, None),), True
        header = payload[offset]
        is_last = bool(header & 0x80)
        block_type = header & 0x7F
        block_size = int.from_bytes(payload[offset + 1 : offset + 4], 'big')
        data_offset = offset + 4
        if len(payload) - data_offset < block_size:
            return findings + (FlacFinding(FlacFindingKind.MALFORMED_CONTAINER, offset, block_size),), True
        kind = _block_kind(block_type)
        findings += (FlacFinding(kind, offset, block_size),)
        if first_block and (block_type != 0 or block_size != 34):
            return findings + (FlacFinding(FlacFindingKind.MALFORMED_CONTAINER, offset, block_size),), True
        first_block = False
        offset = data_offset + block_size
    if payload.endswith(b'TAG' + payload[-125:]) and len(payload) >= 128:
        findings += (FlacFinding(FlacFindingKind.TRAILING_ID3V1, len(payload) - 128, 128),)
    return findings, False


def _leading_id3v2(payload: bytes) -> tuple[int, FlacFinding | None, bool]:
    if not payload.startswith(b'ID3'):
        return 0, None, False
    if len(payload) < 10 or any(value & 0x80 for value in payload[6:10]):
        return 0, None, True
    tag_size = sum(value << (7 * index) for index, value in enumerate(reversed(payload[6:10])))
    total_size = 10 + tag_size
    if total_size > len(payload):
        return 0, None, True
    return total_size, FlacFinding(FlacFindingKind.LEADING_ID3V2, 0, total_size), False


def _block_kind(block_type: int) -> FlacFindingKind:
    match block_type:
        case 0:
            return FlacFindingKind.STREAMINFO
        case 1:
            return FlacFindingKind.PADDING
        case 2:
            return FlacFindingKind.APPLICATION
        case 4:
            return FlacFindingKind.VORBIS_COMMENT
        case 6:
            return FlacFindingKind.PICTURE
        case _:
            return FlacFindingKind.METADATA_BLOCK


def _tool_findings(tool_evidence: ToolEvidence) -> tuple[FlacFinding, ...]:
    match tool_evidence.state:
        case ToolState.SUCCESS:
            return ()
        case ToolState.FAILED:
            return (FlacFinding(FlacFindingKind.FLAC_TEST_FAILED, None, None),)
        case ToolState.MISSING:
            return (FlacFinding(FlacFindingKind.FLAC_TEST_UNAVAILABLE, None, None),)
        case ToolState.EXECUTION_FAILED:
            return (FlacFinding(FlacFindingKind.FLAC_TEST_FAILED, None, None),)
        case ToolState.TIMED_OUT:
            return (FlacFinding(FlacFindingKind.FLAC_TEST_TIMED_OUT, None, None),)
