from __future__ import annotations

from hashlib import sha256
from pathlib import Path
from subprocess import run

from music_ingest.adapters.inspectors.mp3 import InspectionState, Mp3FindingKind, inspect_mp3


def _snapshot(source: Path) -> tuple[int, int, int, str]:
    source_stat = source.stat()
    return source_stat.st_ino, source_stat.st_mtime_ns, source_stat.st_size, sha256(source.read_bytes()).hexdigest()


def _create_mp3(directory: Path, name: str) -> Path:
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
            'libmp3lame',
            str(source),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return source


def test_inspect_mp3_records_id3v2_ffprobe_evidence_and_preserves_source(tmp_path: Path) -> None:
    source = _create_mp3(tmp_path, 'normal.mp3')
    before = _snapshot(source)

    result = inspect_mp3(source)

    assert result.state is InspectionState.VALID
    assert result.ffprobe.return_code == 0
    assert result.id3v2_version is not None
    assert result.id3v2_size is not None
    assert Mp3FindingKind.MP3_FRAME in [finding.kind for finding in result.findings]
    assert _snapshot(source) == before


def test_inspect_mp3_trailing_id3v1_is_evidence_not_a_mutation(tmp_path: Path) -> None:
    source = _create_mp3(tmp_path, 'trailing-id3.mp3')
    _ = source.write_bytes(source.read_bytes() + b'TAG' + (b'\x00' * 125))
    before = _snapshot(source)

    result = inspect_mp3(source)

    assert result.state is InspectionState.VALID
    assert Mp3FindingKind.TRAILING_ID3V1 in [finding.kind for finding in result.findings]
    assert _snapshot(source) == before


def test_inspect_mp3_accepts_run_in_after_declared_id3v23_tag(tmp_path: Path) -> None:
    source = _create_mp3(tmp_path, 'run-in.mp3')
    audio_payload = source.read_bytes()
    tag_size = sum(value << (7 * index) for index, value in enumerate(reversed(audio_payload[6:10])))
    audio_offset = 10 + tag_size
    _ = source.write_bytes(audio_payload[:audio_offset] + b'run-in' + audio_payload[audio_offset:])
    before = _snapshot(source)

    result = inspect_mp3(source)

    assert result.state is InspectionState.VALID
    assert result.id3v2_version is not None
    assert result.id3v2_size is not None
    assert any(
        finding.kind is Mp3FindingKind.MP3_FRAME and finding.offset == audio_offset + 6 for finding in result.findings
    )
    assert _snapshot(source) == before


def test_inspect_mp3_malformed_container_quarantines_and_preserves_source(tmp_path: Path) -> None:
    source = tmp_path / 'truncated.mp3'
    _ = source.write_bytes(b'ID3\x04\x00\x00\x00\x00\x00\x10short')
    before = _snapshot(source)

    result = inspect_mp3(source)

    assert result.state is InspectionState.QUARANTINE
    assert Mp3FindingKind.MALFORMED_CONTAINER in [finding.kind for finding in result.findings]
    assert result.ffprobe.return_code != 0
    assert _snapshot(source) == before
