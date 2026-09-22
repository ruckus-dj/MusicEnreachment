from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from hashlib import sha256
from pathlib import Path
from re import MULTILINE, compile

from pydantic import ValidationError
from sqlalchemy.orm import Session

from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState, run_tool
from music_ingest.adapters.inspectors.flac import FlacInspectionResult
from music_ingest.adapters.inspectors.flac import InspectionState as FlacInspectionState
from music_ingest.adapters.inspectors.mp3 import InspectionState as Mp3InspectionState
from music_ingest.adapters.inspectors.mp3 import Mp3InspectionResult
from music_ingest.contracts import FpcalcPayload
from music_ingest.models import FingerprintRecord
from music_ingest.repositories.persistence import FingerprintRepository
from music_ingest.services.intake.service import SourceId


class FingerprintState(StrEnum):
    SUCCESS = 'success'
    SKIPPED_QUARANTINED = 'skipped_quarantined'
    FAILED = 'failed'
    UNAVAILABLE = 'unavailable'
    EXECUTION_FAILED = 'execution_failed'
    TIMED_OUT = 'timed_out'
    MALFORMED = 'malformed'


@dataclass(frozen=True, slots=True)
class FingerprintRequest:
    source_id: SourceId
    source_path: Path
    inspection: FlacInspectionResult | Mp3InspectionResult | None


@dataclass(frozen=True, slots=True)
class FingerprintResult:
    state: FingerprintState
    fingerprint: str | None
    duration_seconds: float | None
    tool_version: str | None
    output_sha256: str
    tool: ToolEvidence | None
    version_tool: ToolEvidence | None


_VERSION_PATTERN = compile(r'^fpcalc version (?P<version>[^\s]+)', flags=MULTILINE)


def fingerprint_source(
    session: Session,
    request: FingerprintRequest,
    *,
    fpcalc_command: str = 'fpcalc',
    timeout_seconds: float = 30.0,
) -> FingerprintResult:
    cached = FingerprintRepository(session).successful_evidence(request.source_id)
    if cached is not None:
        return FingerprintResult(
            FingerprintState(cached.state),
            cached.fingerprint,
            cached.duration_seconds,
            cached.tool_version,
            cached.output_sha256,
            None,
            None,
        )
    result = calculate_fingerprint(
        request.source_path,
        request.inspection,
        fpcalc_command=fpcalc_command,
        timeout_seconds=timeout_seconds,
    )
    persist_fingerprint(session, request.source_id, result)
    return result


def persist_fingerprint(session: Session, source_id: SourceId, result: FingerprintResult) -> None:
    _ = FingerprintRepository(session).add_evidence(_record(source_id, result))


def calculate_fingerprint(
    source_path: Path,
    inspection: FlacInspectionResult | Mp3InspectionResult | None,
    *,
    fpcalc_command: str = 'fpcalc',
    timeout_seconds: float = 30.0,
) -> FingerprintResult:
    if not _is_valid_inspection(inspection):
        return FingerprintResult(
            FingerprintState.SKIPPED_QUARANTINED, None, None, None, sha256(b'').hexdigest(), None, None
        )
    tool = run_tool((fpcalc_command, '-json', str(source_path)), timeout_seconds)
    return _from_tool(tool, fpcalc_command, timeout_seconds)


def _is_valid_inspection(inspection: FlacInspectionResult | Mp3InspectionResult | None) -> bool:
    match inspection:
        case FlacInspectionResult(state=state):
            return _is_valid_flac_state(state)
        case Mp3InspectionResult(state=state):
            return _is_valid_mp3_state(state)
        case None:
            return True


def _is_valid_flac_state(state: FlacInspectionState) -> bool:
    match state:
        case FlacInspectionState.VALID:
            return True
        case FlacInspectionState.QUARANTINE:
            return False
        case FlacInspectionState.INFRASTRUCTURE:
            return False


def _is_valid_mp3_state(state: Mp3InspectionState) -> bool:
    match state:
        case Mp3InspectionState.VALID:
            return True
        case Mp3InspectionState.QUARANTINE:
            return False
        case Mp3InspectionState.INFRASTRUCTURE:
            return False


def _from_tool(tool: ToolEvidence, fpcalc_command: str, timeout_seconds: float) -> FingerprintResult:
    output_sha256 = sha256(tool.stdout.encode()).hexdigest()
    match tool.state:
        case ToolState.SUCCESS:
            return _parse_success(tool, output_sha256, fpcalc_command, timeout_seconds)
        case ToolState.FAILED:
            return FingerprintResult(FingerprintState.FAILED, None, None, None, output_sha256, tool, None)
        case ToolState.MISSING:
            return FingerprintResult(FingerprintState.UNAVAILABLE, None, None, None, output_sha256, tool, None)
        case ToolState.EXECUTION_FAILED:
            return FingerprintResult(FingerprintState.EXECUTION_FAILED, None, None, None, output_sha256, tool, None)
        case ToolState.TIMED_OUT:
            return FingerprintResult(FingerprintState.TIMED_OUT, None, None, None, output_sha256, tool, None)


def _parse_success(
    tool: ToolEvidence, output_sha256: str, fpcalc_command: str, timeout_seconds: float
) -> FingerprintResult:
    try:
        payload = FpcalcPayload.model_validate_json(tool.stdout)
    except ValidationError:
        return FingerprintResult(FingerprintState.MALFORMED, None, None, None, output_sha256, tool, None)
    version_tool = run_tool((fpcalc_command, '-version'), timeout_seconds)
    version = _version(version_tool)
    if version is None:
        return FingerprintResult(
            _state_from_tool(version_tool),
            payload.fingerprint,
            payload.duration,
            None,
            output_sha256,
            tool,
            version_tool,
        )
    return FingerprintResult(
        FingerprintState.SUCCESS,
        payload.fingerprint,
        payload.duration,
        version,
        output_sha256,
        tool,
        version_tool,
    )


def _version(tool: ToolEvidence) -> str | None:
    match tool.state:
        case ToolState.SUCCESS:
            match = _VERSION_PATTERN.search(tool.stdout)
            return match.group('version') if match is not None else None
        case ToolState.FAILED | ToolState.MISSING | ToolState.EXECUTION_FAILED | ToolState.TIMED_OUT:
            return None


def _state_from_tool(tool: ToolEvidence) -> FingerprintState:
    match tool.state:
        case ToolState.SUCCESS:
            return FingerprintState.MALFORMED
        case ToolState.FAILED:
            return FingerprintState.FAILED
        case ToolState.MISSING:
            return FingerprintState.UNAVAILABLE
        case ToolState.EXECUTION_FAILED:
            return FingerprintState.EXECUTION_FAILED
        case ToolState.TIMED_OUT:
            return FingerprintState.TIMED_OUT


def _record(source_id: SourceId, result: FingerprintResult) -> FingerprintRecord:
    return FingerprintRecord(
        source_id=source_id,
        state=result.state.value,
        fingerprint=result.fingerprint,
        duration_seconds=result.duration_seconds,
        tool_version=result.tool_version,
        output_sha256=result.output_sha256,
        tool_state=result.tool.state.value if result.tool is not None else None,
        return_code=result.tool.return_code if result.tool is not None else None,
        version_tool_state=result.version_tool.state.value if result.version_tool is not None else None,
        version_return_code=result.version_tool.return_code if result.version_tool is not None else None,
        version_output_sha256=sha256(result.version_tool.stdout.encode()).hexdigest()
        if result.version_tool is not None
        else None,
    )
