from __future__ import annotations

import json
from pathlib import Path

import pytest

from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.media_capabilities import MediaCapability, inspect_media_capability


@pytest.mark.parametrize(
    ('codec', 'container'),
    [
        ('flac', 'flac'),
        ('alac', 'mov,mp4,m4a,3gp,3g2,mj2'),
        ('aac', 'mov,mp4,m4a,3gp,3g2,mj2'),
        ('libmp3lame', 'mp3'),
        ('libopus', 'ogg'),
        ('libvorbis', 'ogg'),
    ],
)
def test_inspect_media_capability_accepts_declared_source(
    codec: str, container: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / f'source.{"m4a" if container.startswith("mov") else container}'
    _ = source.write_bytes(b'fixture')
    expected_codec = 'mp3' if codec == 'libmp3lame' else codec.removeprefix('lib')
    probe_output = json.dumps(
        {'streams': [{'codec_type': 'audio', 'codec_name': expected_codec}], 'format': {'format_name': container}}
    )
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )

    result = inspect_media_capability(source)

    assert result.capability is not None
    assert result.capability.container == container
    assert result.capability.codec == expected_codec


@pytest.mark.parametrize('name', ['raw.aac', 'non-vorbis.ogg', 'unknown.bin'])
def test_inspect_media_capability_rejects_undeclared_source(name: str, tmp_path: Path) -> None:
    source = tmp_path / name
    source.write_bytes(b'not a declared media container')

    result = inspect_media_capability(source)

    assert result.capability is None


def test_inspect_media_capability_accepts_any_container_with_one_audio_stream(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'source.wav'
    _ = source.write_bytes(b'fixture')
    probe_output = json.dumps(
        {
            'format': {'format_name': 'wav'},
            'streams': [{'codec_type': 'audio', 'codec_name': 'pcm_s16le'}],
        }
    )
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )

    result = inspect_media_capability(source)

    assert result.capability == MediaCapability('wav', 'pcm_s16le')
    assert result.audio_stream_count == 1


def test_inspect_media_capability_rejects_multiple_audio_streams(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'source.mkv'
    _ = source.write_bytes(b'fixture')
    probe_output = json.dumps(
        {
            'format': {'format_name': 'matroska,webm'},
            'streams': [
                {'codec_type': 'audio', 'codec_name': 'flac'},
                {'codec_type': 'audio', 'codec_name': 'opus'},
            ],
        }
    )
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )

    result = inspect_media_capability(source)

    assert result.capability is None
    assert result.audio_stream_count == 2
