from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import BinaryIO, final, override

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
    audio_start: int
    audio_end: int
    findings: tuple[FlacSanitizationFinding, ...]
    has_trailing_id3v1: bool


def sanitize_flac(request: FlacSanitizationRequest) -> FlacSanitizationResult:
    source_path, output_path, staging_directory = _validated_paths(request)
    source_layout = _parse_layout(source_path)
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
            _ = temporary_file.write(b'fLaC\x80\x00\x00\x22' + source_layout.streaminfo_payload)
            _copy_audio_frames(source_path, source_layout, temporary_file)
            temporary_file.flush()
            os.fsync(temporary_file.fileno())

        output_layout = _parse_layout(temporary_path)
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


def _parse_layout(path: Path) -> _FlacLayout:
    file_size = path.stat().st_size
    with path.open('rb') as source:
        marker_offset, leading_finding = _leading_id3v2(source, path, file_size)
        if source.read(4) != b'fLaC':
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
        offset = marker_offset + 4
        block_number = 0
        findings = (leading_finding,) if leading_finding is not None else ()
        streaminfo_payload: bytes | None = None
        while True:
            header_bytes = source.read(4)
            if len(header_bytes) != 4:
                raise FlacSanitizationFailure(
                    FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path)
                )
            header = header_bytes[0]
            block_type = header & 0x7F
            block_size = int.from_bytes(header_bytes[1:4], 'big')
            data_offset = offset + 4
            block_end = data_offset + block_size
            if block_end > file_size:
                raise FlacSanitizationFailure(
                    FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path)
                )
            if block_number == 0 and (block_type != 0 or block_size != 34):
                raise FlacSanitizationFailure(
                    FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path)
                )
            if block_type == 0:
                if streaminfo_payload is not None:
                    raise FlacSanitizationFailure(
                        FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path)
                    )
                streaminfo_payload = source.read(block_size)
            else:
                findings += (
                    FlacSanitizationFinding(FlacSanitizationFindingKind.METADATA_BLOCK_DROPPED, offset, block_size),
                )
                _ = source.seek(block_size, os.SEEK_CUR)
            offset = block_end
            block_number += 1
            if header & 0x80:
                break
        if streaminfo_payload is None:
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
        if file_size - offset >= 128:
            _ = source.seek(file_size - 128)
            has_trailing_id3v1 = source.read(3) == b'TAG'
        else:
            has_trailing_id3v1 = False
    audio_end = file_size - 128 if has_trailing_id3v1 else file_size
    if audio_end <= offset:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    if has_trailing_id3v1:
        findings += (FlacSanitizationFinding(FlacSanitizationFindingKind.TRAILING_ID3V1_REMOVED, audio_end, 128),)
    return _FlacLayout(
        streaminfo_payload,
        _streaminfo_identity(streaminfo_payload),
        offset,
        audio_end,
        findings,
        has_trailing_id3v1,
    )


def _leading_id3v2(source: BinaryIO, path: Path, file_size: int) -> tuple[int, FlacSanitizationFinding | None]:
    payload = source.read(10)
    if not payload.startswith(b'ID3'):
        _ = source.seek(0)
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
    if total_size > file_size:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    if footer_size:
        _ = source.seek(10 + tag_size)
        if source.read(10) != b'3DI' + payload[3:6] + payload[6:10]:
            raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, path))
    _ = source.seek(total_size)
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


def _copy_audio_frames(source_path: Path, layout: _FlacLayout, output: BinaryIO) -> None:
    remaining = layout.audio_end - layout.audio_start
    with source_path.open('rb') as source:
        _ = source.seek(layout.audio_start)
        while remaining:
            chunk = source.read(min(1024 * 1024, remaining))
            if not chunk:
                raise FlacSanitizationFailure(
                    FlacSanitizationError(FlacSanitizationErrorKind.FRAME_MISMATCH, source_path)
                )
            _ = output.write(chunk)
            remaining -= len(chunk)


def _validate_output_layout(source: _FlacLayout, output: _FlacLayout, temporary_path: Path) -> None:
    if output.streaminfo != source.streaminfo or output.streaminfo_payload != source.streaminfo_payload:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.IDENTITY_MISMATCH, temporary_path)
        )
    if output.audio_end - output.audio_start != source.audio_end - source.audio_start:
        raise FlacSanitizationFailure(FlacSanitizationError(FlacSanitizationErrorKind.FRAME_MISMATCH, temporary_path))
    if output.findings or output.has_trailing_id3v1:
        raise FlacSanitizationFailure(
            FlacSanitizationError(FlacSanitizationErrorKind.MALFORMED_CONTAINER, temporary_path)
        )
