from __future__ import annotations

from hashlib import sha256
from pathlib import Path
from subprocess import run

from music_ingest.inspectors.flac import FlacFindingKind, InspectionState, inspect_flac


def _snapshot(source: Path) -> tuple[int, int, int, str]:
    source_stat = source.stat()
    return source_stat.st_ino, source_stat.st_mtime_ns, source_stat.st_size, sha256(source.read_bytes()).hexdigest()


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


def test_inspect_flac_clean_container_records_blocks_tool_evidence_and_preserves_source(tmp_path: Path) -> None:
    source = _create_flac(tmp_path, 'clean.flac')
    before = _snapshot(source)

    result = inspect_flac(source)

    assert result.state is InspectionState.VALID
    assert result.flac_test.return_code == 0
    assert FlacFindingKind.FLAC_MARKER in [finding.kind for finding in result.findings]
    assert FlacFindingKind.STREAMINFO in [finding.kind for finding in result.findings]
    assert _snapshot(source) == before


def test_inspect_flac_leading_id3v2_is_evidence_not_a_mutation(tmp_path: Path) -> None:
    source = _create_flac(tmp_path, 'leading-id3.flac')
    _ = source.write_bytes(b'ID3\x04\x00\x00\x00\x00\x00\x00' + source.read_bytes())
    before = _snapshot(source)

    result = inspect_flac(source)

    assert result.state is InspectionState.VALID
    assert FlacFindingKind.LEADING_ID3V2 in [finding.kind for finding in result.findings]
    assert _snapshot(source) == before


def test_inspect_flac_trailing_id3v1_is_evidence_not_a_mutation(tmp_path: Path) -> None:
    source = _create_flac(tmp_path, 'trailing-id3.flac')
    _ = source.write_bytes(source.read_bytes() + b'TAG' + (b'\x00' * 125))
    before = _snapshot(source)

    result = inspect_flac(source)

    assert result.state is InspectionState.VALID
    assert FlacFindingKind.TRAILING_ID3V1 in [finding.kind for finding in result.findings]
    assert _snapshot(source) == before


def test_inspect_flac_malformed_container_quarantines_and_preserves_source(tmp_path: Path) -> None:
    source = tmp_path / 'truncated.flac'
    _ = source.write_bytes(b'fLaC\x80\x00\x00')
    before = _snapshot(source)

    result = inspect_flac(source)

    assert result.state is InspectionState.QUARANTINE
    assert FlacFindingKind.MALFORMED_CONTAINER in [finding.kind for finding in result.findings]
    assert result.flac_test.return_code != 0
    assert _snapshot(source) == before


def test_inspect_flac_when_tool_timeout_quarantines_and_preserves_source(tmp_path: Path) -> None:
    source = _create_flac(tmp_path, 'timeout.flac')
    hanging_tool = tmp_path / 'hanging-flac'
    _ = hanging_tool.write_text('#!/bin/sh\nexec sleep 10\n', encoding='utf-8')
    hanging_tool.chmod(0o700)
    before = _snapshot(source)

    result = inspect_flac(source, flac_command=str(hanging_tool), timeout_seconds=0.01)

    assert result.state is InspectionState.QUARANTINE
    assert FlacFindingKind.FLAC_TEST_TIMED_OUT in [finding.kind for finding in result.findings]
    assert result.flac_test.return_code is None
    assert _snapshot(source) == before
