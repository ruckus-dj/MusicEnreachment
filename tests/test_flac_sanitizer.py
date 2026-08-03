from __future__ import annotations

import os
from hashlib import sha256
from pathlib import Path
from subprocess import run

import pytest

from music_ingest.inspectors._tool import ToolState
from music_ingest.sanitizers.flac import FlacSanitizationFailure, FlacSanitizationRequest, sanitize_flac


def _create_flac(directory: Path, name: str) -> Path:
    source = directory / name
    completed = run(  # noqa: S603
        [  # noqa: S607
            'ffmpeg',
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=0.1',
            '-c:a',
            'flac',
            str(source),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return source


def _source_snapshot(path: Path) -> tuple[int, int, int, str]:
    stat = path.stat()
    return stat.st_ino, stat.st_mtime_ns, stat.st_size, sha256(path.read_bytes()).hexdigest()


def _layout(payload: bytes) -> tuple[bytes, bytes, bytes]:
    marker_offset = 0
    if payload.startswith(b'ID3'):
        tag_size = sum(value << (7 * index) for index, value in enumerate(reversed(payload[6:10])))
        marker_offset = 10 + tag_size + (10 if payload[3] == 4 and payload[5] & 0x10 else 0)
    assert payload[marker_offset : marker_offset + 4] == b'fLaC'
    offset = marker_offset + 4
    streaminfo = b''
    while True:
        header = payload[offset]
        size = int.from_bytes(payload[offset + 1 : offset + 4], 'big')
        block = payload[offset + 4 : offset + 4 + size]
        if header & 0x7F == 0:
            streaminfo = block
        offset += 4 + size
        if header & 0x80:
            break
    audio_end = len(payload) - 128 if payload.endswith(b'TAG' + (b'\x00' * 125)) else len(payload)
    return streaminfo, payload[offset:audio_end], payload[marker_offset:offset]


def _streaminfo_identity(streaminfo: bytes) -> tuple[bytes, int, int, int, int]:
    packed = int.from_bytes(streaminfo[10:18], 'big')
    return (
        streaminfo[18:34],
        packed >> 44,
        ((packed >> 41) & 0x07) + 1,
        ((packed >> 36) & 0x1F) + 1,
        packed & ((1 << 36) - 1),
    )


def _add_metadata(source: Path) -> None:
    payload = source.read_bytes()
    streaminfo, audio_frames, _ = _layout(payload)
    vorbis = b'\x00\x00\x00\x00\x00\x00\x00\x00'
    padding = b'\x00' * 16
    application = b'ABCD'
    metadata = (
        bytes((0x00,))
        + len(streaminfo).to_bytes(3, 'big')
        + streaminfo
        + bytes((0x04,))
        + len(vorbis).to_bytes(3, 'big')
        + vorbis
        + bytes((0x01,))
        + len(padding).to_bytes(3, 'big')
        + padding
        + bytes((0x82,))
        + len(application).to_bytes(3, 'big')
        + application
    )
    _ = source.write_bytes(b'fLaC' + metadata + audio_frames)


def _id3v24_footer() -> bytes:
    header = b'ID3\x04\x00\x10\x00\x00\x00\x00'
    return header + b'3DI\x04\x00\x10\x00\x00\x00\x00'


def _assert_sanitized(source: Path, output: Path, source_before: tuple[int, int, int, str]) -> None:
    source_payload = source.read_bytes()
    output_payload = output.read_bytes()
    source_streaminfo, source_frames, _ = _layout(source_payload)
    output_streaminfo, output_frames, output_metadata = _layout(output_payload)
    tested = run(['flac', '-t', str(output)], capture_output=True, check=False, text=True, timeout=10)  # noqa: S603, S607

    assert _source_snapshot(source) == source_before
    assert tested.returncode == 0, tested.stderr
    assert not output_payload.startswith(b'ID3')
    assert not output_payload.endswith(b'TAG' + (b'\x00' * 125))
    assert output_streaminfo == source_streaminfo
    assert _streaminfo_identity(output_streaminfo) == _streaminfo_identity(source_streaminfo)
    assert output_frames == source_frames
    assert output_metadata == b'fLaC\x80\x00\x00\x22' + source_streaminfo


def test_sanitize_flac_preserves_clean_audio_frames_and_streaminfo(tmp_path: Path) -> None:
    # Given: a clean FLAC in an ordinary source location and a private staging directory.
    source = _create_flac(tmp_path, 'clean.flac')
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'clean.flac'

    # When: the container is sanitized into controlled staging.
    result = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: the source is unchanged and the output contains the original audio range only.
    assert result.output_path == output
    assert result.preflight.return_code == 0
    assert result.postflight.return_code == 0
    _assert_sanitized(source, output, source_before)


def test_sanitize_flac_drops_all_non_streaminfo_metadata_blocks(tmp_path: Path) -> None:
    # Given: a FLAC with Vorbis, padding, and application metadata before its frames.
    source = _create_flac(tmp_path, 'metadata.flac')
    _add_metadata(source)
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'metadata.flac'

    # When: the container is sanitized.
    _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: every metadata block except the source STREAMINFO payload is absent.
    _assert_sanitized(source, output, source_before)


@pytest.mark.parametrize(
    ('prefix', 'suffix'),
    [
        (b'ID3\x04\x00\x00\x00\x00\x00\x00', b''),
        (b'', b'TAG' + (b'\x00' * 125)),
    ],
    ids=('leading-id3v2', 'trailing-id3v1'),
)
def test_sanitize_flac_removes_repairable_id3_wrappers(tmp_path: Path, prefix: bytes, suffix: bytes) -> None:
    # Given: a structurally valid FLAC wrapped by one repairable ID3 container.
    source = _create_flac(tmp_path, 'wrapped.flac')
    _ = source.write_bytes(prefix + source.read_bytes() + suffix)
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'wrapped.flac'

    # When: sanitation writes a new staged container.
    _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: the wrapper is removed without changing source frames or source identity.
    _assert_sanitized(source, output, source_before)


def test_sanitize_flac_accepts_a_leading_id3v24_footer_when_preflight_rejects_wrapper(tmp_path: Path) -> None:
    # Given: a FLAC wrapped in a structurally recognized ID3v2.4 footer tag rejected by flac -t.
    source = _create_flac(tmp_path, 'id3v24-footer.flac')
    _ = source.write_bytes(_id3v24_footer() + source.read_bytes())
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'id3v24-footer.flac'

    # When: sanitation rebuilds the container without the repairable wrapper.
    result = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: failed source preflight remains recorded while output and decoded-audio identity validate.
    assert result.preflight.state is ToolState.FAILED
    assert result.preflight.return_code != 0
    assert result.postflight.state is ToolState.SUCCESS
    _assert_sanitized(source, output, source_before)


def test_sanitize_flac_rejects_malformed_input_without_visible_output(tmp_path: Path) -> None:
    # Given: a truncated FLAC source and an empty staging directory.
    source = tmp_path / 'truncated.flac'
    _ = source.write_bytes(b'fLaC\x80\x00\x00')
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'truncated.flac'

    # When: sanitation attempts to parse the malformed container.
    with pytest.raises(FlacSanitizationFailure):
        _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: no staged output is published and the source is intact.
    assert not output.exists()
    assert _source_snapshot(source) == source_before


def test_sanitize_flac_rejects_existing_destination_without_source_mutation(tmp_path: Path) -> None:
    # Given: a valid source and an already occupied final destination.
    source = _create_flac(tmp_path, 'source.flac')
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'existing.flac'
    _ = output.write_bytes(b'occupied')

    # When: sanitation attempts to publish the occupied destination.
    with pytest.raises(FlacSanitizationFailure) as failure:
        _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: the destination and source remain unchanged.
    assert failure.value.error.kind.value == 'destination_conflict'
    assert output.read_bytes() == b'occupied'
    assert _source_snapshot(source) == source_before


def test_sanitize_flac_rejects_output_outside_the_controlled_staging_directory(tmp_path: Path) -> None:
    # Given: a valid source with a staging directory that does not contain the final output.
    source = _create_flac(tmp_path, 'source.flac')
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = tmp_path / 'outside.flac'

    # When: sanitation receives an output outside the supplied staging boundary.
    with pytest.raises(FlacSanitizationFailure):
        _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: it rejects the request before creating an output or modifying the source.
    assert not output.exists()
    assert _source_snapshot(source) == source_before


def test_sanitize_flac_does_not_clobber_destination_created_at_publish_seam(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a valid source and a concurrent actor that creates a victim at link publication.
    source = _create_flac(tmp_path, 'source.flac')
    source_before = _source_snapshot(source)
    staging = tmp_path / 'staging'
    staging.mkdir()
    output = staging / 'race.flac'
    victim = b'concurrent destination'
    original_link = os.link

    def create_victim_then_link(temporary: str, destination: str) -> None:
        _ = output.write_bytes(victim)
        original_link(temporary, destination)

    monkeypatch.setattr('music_ingest.sanitizers.flac.os.link', create_victim_then_link)

    # When: the final destination appears at the no-clobber publication seam.
    with pytest.raises(FlacSanitizationFailure) as failure:
        _ = sanitize_flac(FlacSanitizationRequest(source, output, staging))

    # Then: publication reports a typed conflict and preserves both source and victim byte-for-byte.
    assert failure.value.error.kind.value == 'destination_conflict'
    assert output.read_bytes() == victim
    assert tuple(staging.iterdir()) == (output,)
    assert _source_snapshot(source) == source_before
