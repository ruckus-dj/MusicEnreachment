from __future__ import annotations

import json
from pathlib import Path

import pytest

from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.media_capabilities import MediaCapability, inspect_media_capability


def _successful_probe(monkeypatch: pytest.MonkeyPatch, payload: object) -> None:
    probe_output = json.dumps(payload)
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )


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


def test_inspect_media_capability_preserves_quality_properties_for_selection_policy(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'source.mp3'
    _ = source.write_bytes(b'fixture')
    probe_output = json.dumps(
        {
            'streams': [
                {
                    'codec_type': 'audio',
                    'codec_name': 'mp3',
                    'sample_rate': 44_100,
                    'channels': 2,
                    'bit_rate': 320_000,
                }
            ],
            'format': {'format_name': 'mp3'},
        }
    )
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )

    result = inspect_media_capability(source)

    assert result.technical is not None
    assert result.technical.sample_rate == 44_100
    assert result.technical.channels == 2
    assert result.technical.bitrate == 320_000


def test_inspect_media_capability_uses_raw_bit_depth_when_codec_bit_depth_is_zero(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'source.flac'
    _ = source.write_bytes(b'fixture')
    probe_output = json.dumps(
        {
            'streams': [
                {
                    'codec_type': 'audio',
                    'codec_name': 'flac',
                    'bits_per_sample': 0,
                    'bits_per_raw_sample': '24',
                    'sample_rate': '96000',
                    'channels': 2,
                }
            ],
            'format': {'format_name': 'flac'},
        }
    )
    monkeypatch.setattr(
        'music_ingest.inspectors.media_capabilities.run_tool',
        lambda *_args: ToolEvidence(ToolState.SUCCESS, 0, probe_output, ''),
    )

    result = inspect_media_capability(source)

    assert result.technical is not None
    assert result.technical.bit_depth == 24
    assert result.technical.sample_rate == 96_000


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


@pytest.mark.parametrize(
    'state', [ToolState.FAILED, ToolState.MISSING, ToolState.EXECUTION_FAILED, ToolState.TIMED_OUT]
)
def test_inspect_media_capability_preserves_ffprobe_failure_evidence(
    state: ToolState, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'source.flac'
    evidence = ToolEvidence(state, 23 if state is ToolState.FAILED else None, 'partial output', 'probe failed')
    calls: list[tuple[tuple[str, ...], float]] = []

    def fake_run_tool(command: tuple[str, ...], timeout_seconds: float) -> ToolEvidence:
        calls.append((command, timeout_seconds))
        return evidence

    monkeypatch.setattr('music_ingest.inspectors.media_capabilities.run_tool', fake_run_tool)

    result = inspect_media_capability(source, ffprobe_command='custom-ffprobe', timeout_seconds=2.5)

    assert calls == [
        (
            (
                'custom-ffprobe',
                '-v',
                'error',
                '-show_entries',
                'format=format_name:stream=codec_type,codec_name,bits_per_sample,bits_per_raw_sample,sample_rate,channels,bit_rate',
                '-of',
                'json',
                str(source),
            ),
            2.5,
        )
    ]
    assert result.ffprobe is evidence
    assert result.capability is None
    assert result.audio_stream_count == 0
    assert result.technical is None


@pytest.mark.parametrize(
    ('payload', 'expected_audio_stream_count'),
    [
        ([], 0),
        ({}, 0),
        ({'format': [], 'streams': []}, 0),
        ({'format': {'format_name': 'flac'}, 'streams': {}}, 0),
        ({'format': {'format_name': 42}, 'streams': [{'codec_type': 'audio', 'codec_name': 'flac'}]}, 1),
        ({'format': {'format_name': 'flac'}, 'streams': [None, 'audio', {'codec_type': 'video'}]}, 0),
        ({'format': {'format_name': 'flac'}, 'streams': [{'codec_type': 'audio', 'codec_name': 42}]}, 0),
    ],
)
def test_inspect_media_capability_rejects_malformed_probe_structures(
    payload: object, expected_audio_stream_count: int, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _successful_probe(monkeypatch, payload)

    result = inspect_media_capability(tmp_path / 'source.bin')

    assert result.capability is None
    assert result.audio_stream_count == expected_audio_stream_count
    assert result.technical is None


def test_inspect_media_capability_publication_only_rejects_otherwise_valid_capability(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _successful_probe(
        monkeypatch,
        {
            'format': {'format_name': 'wav'},
            'streams': [{'codec_type': 'audio', 'codec_name': 'pcm_s16le', 'sample_rate': '48000'}],
        },
    )

    result = inspect_media_capability(tmp_path / 'source.wav', publication_only=True)

    assert result.capability is None
    assert result.audio_stream_count == 1
    assert result.technical is None


def test_inspect_media_capability_publication_only_accepts_declared_capability(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _successful_probe(
        monkeypatch,
        {
            'format': {'format_name': 'matroska,webm'},
            'streams': [{'codec_type': 'audio', 'codec_name': 'opus', 'sample_rate': '48000'}],
        },
    )

    result = inspect_media_capability(tmp_path / 'source.mka', publication_only=True)

    assert result.capability == MediaCapability('matroska,webm', 'opus')
    assert result.audio_stream_count == 1
    assert result.technical is not None
    assert result.technical.sample_rate == 48_000


def test_inspect_media_capability_parses_only_positive_numeric_quality_values(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _successful_probe(
        monkeypatch,
        {
            'format': {'format_name': 'flac'},
            'streams': [
                {
                    'codec_type': 'audio',
                    'codec_name': 'flac',
                    'bits_per_sample': '-1',
                    'bits_per_raw_sample': '20',
                    'sample_rate': ' 96000 ',
                    'channels': 6,
                    'bit_rate': 'not-a-number',
                }
            ],
        },
    )

    result = inspect_media_capability(tmp_path / 'source.flac')

    assert result.technical is not None
    assert result.technical.bit_depth == 20
    assert result.technical.sample_rate == 96_000
    assert result.technical.channels == 6
    assert result.technical.bitrate is None


@pytest.mark.parametrize('value', [True, False, 0, -1, 1.5, None, 'N/A'])
def test_inspect_media_capability_does_not_invent_quality_from_invalid_numeric_facts(
    value: object, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _successful_probe(
        monkeypatch,
        {
            'format': {'format_name': 'flac'},
            'streams': [
                {
                    'codec_type': 'audio',
                    'codec_name': 'flac',
                    'bits_per_sample': value,
                    'bits_per_raw_sample': value,
                    'sample_rate': value,
                    'channels': value,
                    'bit_rate': value,
                }
            ],
        },
    )
    result = inspect_media_capability(tmp_path / 'source.flac')
    assert result.capability == MediaCapability('flac', 'flac')
    assert result.technical is not None
    assert result.technical.bit_depth is None
    assert result.technical.sample_rate is None
    assert result.technical.channels is None
    assert result.technical.bitrate is None
