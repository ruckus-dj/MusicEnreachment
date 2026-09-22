from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import final, override

from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState
from music_ingest.adapters.inspectors.decoder import decoder_evidence


class FlacSanitizationErrorKind(StrEnum):
    MALFORMED_CONTAINER = 'malformed_container'
    INVALID_STAGING = 'invalid_staging'
    DESTINATION_CONFLICT = 'destination_conflict'
    SOURCE_DESTINATION_COLLISION = 'source_destination_collision'
    PREFLIGHT_FAILED = 'preflight_failed'
    POSTFLIGHT_FAILED = 'postflight_failed'
    IDENTITY_MISMATCH = 'identity_mismatch'
    FRAME_MISMATCH = 'frame_mismatch'


class FlacSanitizationFindingKind(StrEnum):
    LEADING_ID3V2_REMOVED = 'leading_id3v2_removed'
    TRAILING_ID3V1_REMOVED = 'trailing_id3v1_removed'
    METADATA_BLOCK_DROPPED = 'metadata_block_dropped'


@dataclass(frozen=True, slots=True)
class FlacSanitizationError:
    kind: FlacSanitizationErrorKind
    path: Path

    @override
    def __str__(self) -> str:
        return f'{self.kind}: {self.path}'


@final
class FlacSanitizationFailure(Exception):
    error: FlacSanitizationError

    def __init__(self, error: FlacSanitizationError) -> None:
        super().__init__(str(error))
        self.error = error


@dataclass(frozen=True, slots=True)
class FlacSanitizationFinding:
    kind: FlacSanitizationFindingKind
    offset: int
    size: int


@dataclass(frozen=True, slots=True)
class FlacSanitizationRequest:
    source_path: Path
    output_path: Path
    staging_directory: Path
    ffmpeg_command: str = 'ffmpeg'
    timeout_seconds: float = 10.0


@dataclass(frozen=True, slots=True)
class FlacStreamIdentity:
    md5: bytes
    sample_rate: int
    channels: int
    bits_per_sample: int
    total_samples: int


@dataclass(frozen=True, slots=True)
class FlacSanitizationResult:
    output_path: Path
    findings: tuple[FlacSanitizationFinding, ...]
    preflight: ToolEvidence
    postflight: ToolEvidence
    streaminfo: FlacStreamIdentity


@dataclass(frozen=True, slots=True)
class _FlacLayout:
    streaminfo_payload: bytes
    streaminfo: FlacStreamIdentity
    audio_frames: bytes
    findings: tuple[FlacSanitizationFinding, ...]
    has_trailing_id3v1: bool


def sanitize_flac(request: FlacSanitizationRequest) -> FlacSanitizationResult:
    source_path, output_path, staging_directory = _validated_paths(request)
    payload = source_path.read_bytes()
    source_layout = _parse_layout(payload, source_path)
    preflight = decoder_evidence(
        source_path, ffmpeg_command=request.ffmpeg_command, timeout_seconds=request.timeout_seconds
    )
    has_repairable_wrapper = source_layout.has_trailing_id3v1 or any(
        finding.kind is FlacSanitizationFindingKind.LEADING_ID3V2_REMOVED for finding in source_layout.findings
    )
    if preflight.state is not ToolState.SUCCESS and not (
        preflight.state is ToolState.FAILED and has_repairable_wrapper
    ):
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.PREFLIGHT_FAILED, source_path))

    temporary_path: Path | None = None
    try:
        file_descriptor, temporary_name = tempfile.mkstemp(
            prefix='.flac-sanitize-', suffix='.tmp', dir=staging_directory
        )
        temporary_path = Path(temporary_name)
        with os.fdopen(file_descriptor, 'wb') as temporary_file:
            _ = temporary_file.write(_sanitized_payload(source_layout))
            temporary_file.flush()
            os.fsync(temporary_file.fileno())

        sanitized_payload = temporary_path.read_bytes()
        output_layout = _parse_layout(sanitized_payload, temporary_path)
        _validate_output_layout(source_layout, output_layout, temporary_path)
        postflight = decoder_evidence(
            temporary_path, ffmpeg_command=request.ffmpeg_command, timeout_seconds=request.timeout_seconds
        )
        if postflight.state is not ToolState.SUCCESS:
            raise FlacSanitizationFailure(
                FlacSanitizationError(FlacSanitizationErrorKind.POSTFLIGHT_FAILED, temporary_path)
            )
        try:
            _copy_file_exclusive(temporary_path, output_path)
        except FileExistsError:
            raise FlacSanitizationFailure(
                FlacSanitizationError(FlacSanitizationErrorKind.DESTINATION_CONFLICT, output_path)
            ) from None
        temporary_path.unlink()
        return FlacSanitizationResult(
            output_path, source_layout.findings, preflight, postflight, source_layout.streaminfo
        )
    finally:
        if temporary_path is not None and temporary_path.exists():
            temporary_path.unlink()


def _validated_paths(request: FlacSanitizationRequest) -> tuple[Path, Path, Path]:
    source_path = request.source_path.resolve(strict=True)
    output_path = request.output_path.resolve()
    staging_directory = request.staging_directory.resolve(strict=True)
    if not staging_directory.is_dir() or output_path.parent != staging_directory:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.INVALID_STAGING, request.staging_directory)
        )
    if source_path == output_path:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.SOURCE_DESTINATION_COLLISION, output_path)
        )
    if output_path.exists():
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.DESTINATION_CONFLICT, output_path)
        )
    return source_path, output_path, staging_directory


def _copy_file_exclusive(source: Path, destination: Path) -> None:
    descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    try:
        with os.fdopen(descriptor, 'wb') as output, source.open('rb') as input_file:
            shutil.copyfileobj(input_file, output)
            output.flush()
            os.fsync(output.fileno())
    except OSError:
        destination.unlink(missing_ok=True)
        raise


def _parse_layout(payload: bytes, path: Path) -> _FlacLayout:
    marker_offset, leading_finding = _leading_id3v2(payload, path)
    if payload[marker_offset : marker_offset + 4] != b'fLaC':
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    offset = marker_offset + 4
    block_number = 0
    findings = (leading_finding,) if leading_finding is not None else ()
    streaminfo_payload: bytes | None = None
    while True:
        if len(payload) - offset < 4:
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
        header = payload[offset]
        block_type = header & 0x7F
        block_size = int.from_bytes(payload[offset + 1 : offset + 4], 'big')
        data_offset = offset + 4
        block_end = data_offset + block_size
        if block_end > len(payload):
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
        block_payload = payload[data_offset:block_end]
        if block_number == 0 and (block_type != 0 or block_size != 34):
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
        if block_type == 0:
            if streaminfo_payload is not None:
                raise FlacSanitizationFailure(
                    FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path)
                )
            streaminfo_payload = block_payload
        else:
            findings += (
                FlacSanitizationFinding(FlacSanitizationFindingKind.METADATA_BLOCK_DROPPED, offset, block_size),
            )
        offset = block_end
        block_number += 1
        if header & 0x80:
            break
    if streaminfo_payload is None:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    has_trailing_id3v1 = len(payload) - offset >= 128 and payload[-128:-125] == b'TAG'
    audio_end = len(payload) - 128 if has_trailing_id3v1 else len(payload)
    if audio_end <= offset:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    if has_trailing_id3v1:
        findings += (FlacSanitizationFinding(FlacSanitizationFindingKind.TRAILING_ID3V1_REMOVED, audio_end, 128),)
    return _FlacLayout(
        streaminfo_payload,
        _streaminfo_identity(streaminfo_payload),
        payload[offset:audio_end],
        findings,
        has_trailing_id3v1,
    )


def _leading_id3v2(payload: bytes, path: Path) -> tuple[int, FlacSanitizationFinding | None]:
    if not payload.startswith(b'ID3'):
        return 0, None
    if len(payload) < 10 or payload[3] not in (2, 3, 4) or payload[5] & 0x0F:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    size_bytes = payload[6:10]
    if any(value & 0x80 for value in size_bytes):
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    tag_size = int.from_bytes(size_bytes, 'big')
    tag_size = (
        ((tag_size & 0x7F000000) >> 3)
        | ((tag_size & 0x007F0000) >> 2)
        | ((tag_size & 0x00007F00) >> 1)
        | (tag_size & 0x0000007F)
    )
    footer_size = 10 if payload[3] == 4 and payload[5] & 0x10 else 0
    total_size = 10 + tag_size + footer_size
    if total_size > len(payload) or (
        footer_size and payload[10 + tag_size : total_size] != b'3DI' + payload[3:6] + payload[6:10]
    ):
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    return total_size, FlacSanitizationFinding(FlacSanitizationFindingKind.LEADING_ID3V2_REMOVED, 0, total_size)


def _streaminfo_identity(streaminfo_payload: bytes) -> FlacStreamIdentity:
    packed = int.from_bytes(streaminfo_payload[10:18], 'big')
    return FlacStreamIdentity(
        streaminfo_payload[18:34],
        packed >> 44,
        ((packed >> 41) & 0x07) + 1,
        ((packed >> 36) & 0x1F) + 1,
        packed & ((1 << 36) - 1),
    )


def _sanitized_payload(layout: _FlacLayout) -> bytes:
    return b'fLaC\x80\x00\x00\x22' + layout.streaminfo_payload + layout.audio_frames


def _validate_output_layout(source: _FlacLayout, output: _FlacLayout, temporary_path: Path) -> None:
    if output.streaminfo != source.streaminfo or output.streaminfo_payload != source.streaminfo_payload:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.IDENTITY_MISMATCH, temporary_path)
        )
    if output.audio_frames != source.audio_frames:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.FRAME_MISMATCH, temporary_path))
    if output.findings or output.has_trailing_id3v1:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, temporary_path)
        )
