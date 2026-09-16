import asyncio
from hashlib import sha256
from pathlib import Path

import pytest
from mutagen.asf import ASF
from mutagen.flac import FLAC
from mutagen.id3 import ID3, TIT2, TPE1


@pytest.mark.parametrize('multi_value', [False, True])
def test_id3_rejects_aggregate_evidence_amplification(multi_value: bool) -> None:
    from music_ingest.normalize.source_evidence import _id3_fields
    from music_ingest.normalize.tags import MetadataTagError

    payload = b'\x00' + (b'a\0' * 4000 if multi_value else b'a')
    frame = b'TIT2' + len(payload).to_bytes(4, 'big') + b'\0\0' + payload
    body = frame if multi_value else frame * 2000
    with pytest.raises(MetadataTagError, match='aggregate evidence'):
        _id3_fields(body, 3, 0 if multi_value else 0x80)


def test_id3_rejects_excessive_field_count() -> None:
    from music_ingest.normalize.source_evidence import _id3_fields
    from music_ingest.normalize.tags import MetadataTagError

    body = b'TIT2' + (5001).to_bytes(4, 'big') + b'\0\0' + b'\x00' + b'\0' * 5000
    with pytest.raises(MetadataTagError, match='field count'):
        _id3_fields(body, 3, 0)


def test_evidence_limit_does_not_attach_partial_observations(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session

    from music_ingest.models import Base, SourceRecord
    from music_ingest.normalize.tags import MetadataTagError
    from music_ingest.processing import ProcessingConfig
    from music_ingest.processing.support.evidence import SourceEvidence
    from music_ingest.processing.support.settings import RuntimeProcessingSettings

    frame = b'TIT2' + (2).to_bytes(4, 'big') + b'\0\0\x00a'
    body = frame * 2000
    size = bytes((len(body) >> shift) & 127 for shift in (21, 14, 7, 0))
    path = tmp_path / 'large.mp3'
    original = b'ID3\x03\x00\x80' + size + body
    path.write_bytes(original)
    monkeypatch.setattr('music_ingest.normalize.source_evidence.File', lambda *args, **kwargs: None)
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = SourceRecord(id='source')
        config = ProcessingConfig(tmp_path, tmp_path / 'stage', tmp_path / 'out')
        evidence = SourceEvidence(session, config, RuntimeProcessingSettings(session, config))
        with pytest.raises(MetadataTagError, match='aggregate evidence'):
            evidence.capture_observations(source, path, ())
        assert not source.tag_observations
        assert not session.new
    assert path.read_bytes() == original


def test_utf16_txxx_inherits_description_bom() -> None:
    from music_ingest.normalize.source_evidence import _id3_fields

    text = 'MusicBrainz Album Id'.encode('utf-16-le')
    value = 'release-id'.encode('utf-16-le')
    payload = b'\x01\xff\xfe' + text + b'\0\0' + value + b'\0\0'
    frame = b'TXXX' + len(payload).to_bytes(4, 'big') + b'\0\0' + payload
    fields = _id3_fields(frame, 3, 0)
    assert fields[0].value == 'release-id'
    assert fields[0].declared_codec == 'utf-16-le'


@pytest.mark.parametrize('container', ['flac', 'asf', 'id3v2', 'id3v1'])
def test_import_evidence_actual_audio_immutable(tmp_path: Path, container: str) -> None:
    from music_ingest.normalize.source_evidence import read_source_fields

    extension, codec = {
        'flac': ('flac', 'flac'),
        'asf': ('wma', 'wmav2'),
        'id3v2': ('mp3', 'libmp3lame'),
        'id3v1': ('mp3', 'libmp3lame'),
    }[container]
    path = tmp_path / f'audio.{extension}'

    async def generate() -> None:
        process = await asyncio.create_subprocess_exec(
            'ffmpeg',
            '-v',
            'error',
            '-f',
            'lavfi',
            '-i',
            'anullsrc=r=44100:cl=stereo',
            '-t',
            '0.15',
            '-c:a',
            codec,
            str(path),
        )
        assert await process.wait() == 0

    asyncio.run(generate())
    if container == 'flac':
        tags = FLAC(path)
        tags['title'] = 'Привет'
        tags['artist'] = 'Beyoncé'
        tags.save()
    elif container == 'asf':
        tags = ASF(path)
        tags['Title'] = 'Привет'
        tags['Author'] = 'Beyoncé'
        tags.save()
    elif container == 'id3v2':
        tags = ID3(path)
        tags.add(TIT2(encoding=3, text=['Привет']))
        tags.add(TPE1(encoding=1, text=['Beyoncé']))
        tags.save(path)
    else:
        ID3(path).delete(path, delete_v1=True, delete_v2=True)
        with path.open('ab') as stream:
            stream.write(
                b'TAG'
                + 'Привет'.encode('cp1251').ljust(30, b'\0')
                + b'Beyonc\xe9'.ljust(30, b'\0')
                + b'Album'.ljust(30, b'\0')
                + b'2000'
                + b'\0' * 30
                + b'\xff'
            )
    before = sha256(path.read_bytes()).hexdigest()
    fields = read_source_fields(path)
    assert sha256(path.read_bytes()).hexdigest() == before
    title = next(field for field in fields if field.tag_name == 'TITLE')
    assert title.prepared_bytes is not None
    assert title.binary_evidence is not None
    assert title.physical_id
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session

    from music_ingest.models import Base, SourceRecord
    from music_ingest.processing import ProcessingConfig
    from music_ingest.processing.support.evidence import SourceEvidence
    from music_ingest.processing.support.settings import RuntimeProcessingSettings

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        config = ProcessingConfig(tmp_path, tmp_path / 'stage', tmp_path / 'out')
        source = SourceRecord(id='source')
        SourceEvidence(session, config, RuntimeProcessingSettings(session, config)).capture_observations(
            source, path, ()
        )
        assert any(item.prepared_bytes is not None for item in source.tag_observations)
    if container == 'id3v1':
        assert title.prepared_bytes.decode('cp1251') == 'Привет'
    else:
        assert title.value == 'Привет'
    assert next(field for field in fields if field.tag_name == 'ARTIST').value == 'Beyoncé'
