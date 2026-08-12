from __future__ import annotations

import json
from pathlib import Path

import pytest

from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.media_capabilities import inspect_media_capability


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
    probe_output = json.dumps({'streams': [{'codec_name': expected_codec}], 'format': {'format_name': container}})
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
