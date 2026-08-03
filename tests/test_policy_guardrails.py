from __future__ import annotations

import socket
from pathlib import Path
from shutil import copytree

import pytest
from pydantic import ValidationError

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run
from music_ingest.config.policies import load_policy_bundle
from music_ingest.integrations.lidarr import LidarrClient, LidarrIntakeEvent, enqueue_lidarr_intake
from music_ingest.matching.providers import ProductionTransportDisabledError, build_live_transport

_POLICY_FIXTURES = Path(__file__).parent / 'fixtures' / 'policies' / 'valid'


def test_policy_guardrails_when_unsafe_provider_and_sidecar_settings_are_loaded_reject_them(tmp_path: Path) -> None:
    # Given: otherwise valid operator policy files with local metadata and embedded sidecars requested.
    policy_directory = tmp_path / 'policies'
    _ = copytree(_POLICY_FIXTURES, policy_directory)
    providers = policy_directory / 'metadata-providers.yaml'
    provider_policy = providers.read_text(encoding='utf-8').replace(
        'https://musicbrainz.org/ws/2/', 'http://localhost/ws/2/'
    )
    _ = providers.write_text(provider_policy, encoding='utf-8')

    # When: configuration parses the unsafe provider target.
    with pytest.raises(ValidationError):
        _ = load_policy_bundle(policy_directory)

    # Then: a local provider endpoint cannot enter the runtime policy.


def test_policy_guardrails_when_embedded_sidecars_are_requested_reject_them(tmp_path: Path) -> None:
    # Given: otherwise valid operator policy files with embedded lyrics requested.
    policy_directory = tmp_path / 'policies'
    _ = copytree(_POLICY_FIXTURES, policy_directory)
    lyrics = policy_directory / 'lyrics-policy.yaml'
    lyrics_policy = lyrics.read_text(encoding='utf-8').replace('embedded_lyrics: false', 'embedded_lyrics: true')
    _ = lyrics.write_text(lyrics_policy, encoding='utf-8')

    # When: configuration parses the embedded-lyrics policy.
    with pytest.raises(ValidationError):
        _ = load_policy_bundle(policy_directory)

    # Then: the runtime policy remains external-sidecar only.


def test_policy_guardrails_when_default_suite_constructs_live_transport_denies_egress(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: no explicit production transport opt-in.
    monkeypatch.delenv('MUSIC_INGEST_ENABLE_LIVE_TRANSPORT', raising=False)

    # When: the default suite attempts production client construction or a socket connection.
    with pytest.raises(ProductionTransportDisabledError):
        _ = build_live_transport()
    with pytest.raises(RuntimeError, match='network access denied'):
        _ = socket.create_connection(('127.0.0.1', 9), timeout=0.01)

    # Then: neither operation can reach a public provider.


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
