from __future__ import annotations

from pathlib import Path

import pytest

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run
from tests.support.lidarr import LidarrClient, LidarrIntakeEvent, enqueue_lidarr_intake


def test_policy_guardrails_when_dry_run_targets_source_tree_rejects_source_mutation(tmp_path: Path) -> None:
    # Given: an audio source directory that has no external report location.
    source_directory = tmp_path / 'library'
    source_directory.mkdir()
    _ = (source_directory / 'sample.flac').write_bytes(b'fixture')

    # When: dry-run output is requested inside that source tree.
    with pytest.raises(DryRunMutationError, match='outside the source tree'):
        _ = run_dry_run(source_directory, source_directory / 'reports')

    # Then: the read-only source boundary rejects the write before scanning.


def test_policy_guardrails_when_lidarr_intake_is_enqueued_retains_incoming_pathname(tmp_path: Path) -> None:
    # Given: a real incoming file and an in-memory command transport.
    calls: list[tuple[str, dict[str, str], dict[str, list[str] | str], float]] = []
    incoming_directory = tmp_path / 'incoming' / 'Artist' / 'Release'
    incoming_directory.mkdir(parents=True)
    incoming_file = incoming_directory / '01.flac'
    _ = incoming_file.write_bytes(b'raw incoming bytes')
    incoming_before = incoming_file.read_bytes(), incoming_file.stat().st_ino, incoming_file.stat().st_mtime_ns

    class RecordingTransport:
        def post(self, url: str, *, headers: dict[str, str], json: dict[str, list[str] | str], timeout: float) -> None:
            calls.append((url, headers, json, timeout))

    event = LidarrIntakeEvent(str(incoming_file), 'source-id')
    client = LidarrClient('http://lidarr.test', 'fixture-key', RecordingTransport())

    # When: intake requests Lidarr's rescan.
    result = enqueue_lidarr_intake(event, client)

    # Then: the original incoming pathname and file are retained and only its folder is rescanned.
    assert result == event
    assert incoming_before == (
        incoming_file.read_bytes(),
        incoming_file.stat().st_ino,
        incoming_file.stat().st_mtime_ns,
    )
    assert calls == [
        (
            'http://lidarr.test/api/v1/command',
            {'X-Api-Key': 'fixture-key'},
            {'name': 'RescanFolders', 'folders': [str(incoming_directory)]},
            10.0,
        )
    ]
