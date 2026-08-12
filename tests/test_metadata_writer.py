from __future__ import annotations

from dataclasses import replace
from hashlib import sha256
from pathlib import Path
from subprocess import run

import pytest
from mutagen.flac import FLAC
from mutagen.id3 import ID3, TIT2
from mutagen.mp4 import MP4
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis

from music_ingest.dto import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.metadata import (
    CanonicalMetadata,
    CanonicalSource,
    MetadataWriteError,
    MetadataWriteRequest,
    write_canonical_metadata,
)
from music_ingest.normalize.tags import read_normalized_tags


def _create_flac(directory: Path, name: str) -> Path:
    source = directory / name
    completed = run(  # noqa: S603,S607
        [  # noqa: S607
            'ffmpeg',  # noqa: S607
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-ac',
            '2',
            '-c:a',
            'flac',
            str(source),
        ],  # noqa: S607,E501
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return source


def _create_mp3(directory: Path, name: str) -> Path:
    source = directory / name
    payload = b'\xff\xfb\x90\x64' + b'\x00' * 417
    _ = source.write_bytes(payload)
    tags = ID3()
    tags.save(source)
    with source.open('ab') as handle:
        handle.write(payload)
    return source


def _create_m4a(directory: Path, name: str) -> Path:
    source = directory / name
    completed = run(  # noqa: S603,S607
        [  # noqa: S607
            'ffmpeg',  # noqa: S607
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-ac',
            '2',
            '-c:a',
            'aac',
            str(source),
        ],  # noqa: S607,E501
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return source


def _create_ogg(directory: Path, name: str, codec: str) -> Path:
    source = directory / name
    completed = run(  # noqa: S603,S607
        [  # noqa: S607
            'ffmpeg',  # noqa: S607
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-ac',
            '2',
            '-c:a',
            codec,
            *(('-strict', '-2') if codec == 'vorbis' else ()),
            str(source),
        ],  # noqa: S607,E501
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return source


def _policies() -> tuple[FieldPolicy, GenrePolicy]:
    return (
        FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=tuple(sorted(ALLOWED_TAG_KEYS))),
        GenrePolicy(
            schema_version=1,
            canonical_genres=('Hip Hop', 'Alternative Rock'),
            aliases={'rap rock': ('Hip Hop', 'Alternative Rock'), 'rock rap': ('Hip Hop', 'Alternative Rock')},
        ),  # noqa: E501
    )


def _metadata(source: CanonicalSource = CanonicalSource.VERIFIED_RELEASE) -> CanonicalMetadata:
    return CanonicalMetadata(
        source=source,
        title='Fixture Track',
        artists=('Artist One', 'Artist Two'),
        album='Fixture Release',
        album_artists=('Artist One', 'Artist Two'),
        date='2026-07-28',
        original_date=None,
        track_number=1,
        track_total=2,
        disc_number=1,
        disc_total=1,
        genres=('Rock Rap',),
        musicbrainz_track_id='track-id' if source is CanonicalSource.VERIFIED_RELEASE else None,
        musicbrainz_album_id='album-id' if source is CanonicalSource.VERIFIED_RELEASE else None,
        musicbrainz_release_group_id='group-id' if source is CanonicalSource.VERIFIED_RELEASE else None,
        isrc='USFIX2600001',
    )


def test_write_canonical_metadata_when_verified_facts_writes_allowlisted_tags(tmp_path: Path) -> None:
    # Given: sanitized audio and a verified release decision rather than source observations.
    source = _create_flac(tmp_path, 'source.flac')
    snapshot = source.stat().st_ino, sha256(source.read_bytes()).hexdigest()
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()

    # When: the canonical decision is written into controlled staging.
    output = write_canonical_metadata(
        MetadataWriteRequest(source, staging / 'track.flac', staging, _metadata(), fields, genres)
    )

    # Then: list fields use semicolons, aliases are ordered canonically, and source bytes are unchanged.
    listed = dict(read_normalized_tags(output.output_path))
    assert listed['TITLE'] == 'Fixture Track'
    assert listed['ARTIST'] == 'Artist One; Artist Two'
    assert listed['GENRE'] == 'Hip Hop; Alternative Rock'
    assert listed['MUSICBRAINZ_ALBUMID'] == 'album-id'
    assert '/' not in listed['GENRE']
    assert snapshot == (source.stat().st_ino, sha256(source.read_bytes()).hexdigest())


@pytest.mark.parametrize(
    ('suffix', 'codec'),
    (('.flac', 'flac'), ('.m4a', 'alac'), ('.m4a', 'aac'), ('.mp3', 'mp3'), ('.opus', 'opus'), ('.ogg', 'vorbis')),
)
def test_write_canonical_metadata_copies_declared_capability_before_canonical_reopen(
    tmp_path: Path, suffix: str, codec: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    observed = source.read_bytes()
    monkeypatch.setattr(
        'music_ingest.normalize.metadata.inspect_media_capability',
        lambda _path: type('Inspection', (), {'capability': type('Capability', (), {'codec': codec})()})(),
    )
    monkeypatch.setattr('music_ingest.normalize.metadata._write_vorbis_comments', lambda *_args: None)
    monkeypatch.setattr('music_ingest.normalize.metadata._verify_vorbis_comments', lambda *_args: None)
    monkeypatch.setattr('music_ingest.normalize.metadata._write_mp3_tags', lambda *_args: None)
    monkeypatch.setattr('music_ingest.normalize.metadata._verify_mp3_tags', lambda *_args: None)
    monkeypatch.setattr('music_ingest.normalize.metadata._write_mp4_tags', lambda *_args: None)
    monkeypatch.setattr('music_ingest.normalize.metadata._verify_mp4_tags', lambda *_args: None)

    result = write_canonical_metadata(
        MetadataWriteRequest(source, staging / f'track{suffix}', staging, _metadata(), fields, genres)
    )

    assert result.output_path.suffix == suffix
    assert result.output_path.read_bytes() == observed


def test_write_canonical_metadata_rejects_unsupported_capability_before_staging(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    monkeypatch.setattr(
        'music_ingest.normalize.metadata.inspect_media_capability',
        lambda _path: type('Inspection', (), {'capability': None})(),
    )

    with pytest.raises(MetadataWriteError, match='declared capability'):
        _ = write_canonical_metadata(
            MetadataWriteRequest(source, staging / 'track.flac', staging, _metadata(), fields, genres)
        )

    assert not list(staging.glob('.metadata-*'))


def test_write_canonical_metadata_when_source_has_observed_tags_reopens_with_mutagen_and_writes_canonical_tags(
    tmp_path: Path,
) -> None:
    # Given: a FLAC source whose observed tags conflict with the canonical decision.
    source = _create_flac(tmp_path, 'source.flac')
    source_tags = FLAC(source)
    source_tags['TITLE'] = ['Observed title']
    source_tags['GENRE'] = ['Observed genre']
    source_tags.save()
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()

    # When: canonical metadata is written to a distinct staged FLAC.
    output = write_canonical_metadata(
        MetadataWriteRequest(source, staging / 'track.flac', staging, _metadata(), fields, genres)
    )

    # Then: Mutagen observes only the canonical contract, not source-observed values.
    observed = FLAC(output.output_path)
    assert observed['TITLE'] == ['Fixture Track']
    assert observed['GENRE'] == ['Hip Hop; Alternative Rock']
    assert observed['ARTIST'] == ['Artist One; Artist Two']
    assert 'Observed title' not in observed['TITLE']
    assert 'Observed genre' not in observed['GENRE']


def test_write_canonical_metadata_when_mp3_source_has_observed_tags_reopens_with_canonical_id3_frames(
    tmp_path: Path,
) -> None:
    # Given: a minimal valid MP3 with source-observed ID3 values.
    source = _create_mp3(tmp_path, 'source.mp3')
    observed = ID3(source)
    observed.add(TIT2(encoding=3, text='Observed title'))
    observed.save()
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    metadata = replace(_metadata(), original_date='2020-01-01')

    # When: canonical metadata is written to a distinct staged MP3.
    output = write_canonical_metadata(
        MetadataWriteRequest(source, staging / 'track.mp3', staging, metadata, fields, genres)
    )

    # Then: reopened ID3 contains only canonical contract frames and MusicBrainz UFID.
    tags = ID3(output.output_path)
    assert tags['TIT2'].text == ['Fixture Track']
    assert tags['TPE1'].text == ['Artist One', 'Artist Two']
    assert tags['TPE2'].text == ['Artist One', 'Artist Two']
    assert tags['TALB'].text == ['Fixture Release']
    assert tags['TRCK'].text == ['1/2']
    assert tags['TPOS'].text == ['1/1']
    assert str(tags['TDRC'].text[0]) == '2026-07-28'
    assert str(tags['TDOR'].text[0]) == '2020-01-01'
    assert tags['TCON'].text == ['Rock Rap']
    assert tags['TSRC'].text == ['USFIX2600001']
    assert tags['UFID:musicbrainz.org'].data == b'track-id'
    assert 'Observed title' not in tags['TIT2'].text


def test_write_canonical_metadata_when_m4a_source_has_observed_tags_reopens_with_canonical_mp4_atoms(
    tmp_path: Path,
) -> None:
    # Given: a minimal M4A with source-observed MP4 tags.
    source = _create_m4a(tmp_path, 'source.m4a')
    observed = MP4(source)
    observed['©nam'] = ['Observed title']
    observed['----:com.apple.iTunes:source'] = [b'observed']
    observed.save()
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    metadata = replace(_metadata(), original_date='2020-01-01')

    # When: canonical metadata is written to a distinct staged M4A.
    output = write_canonical_metadata(
        MetadataWriteRequest(source, staging / 'track.m4a', staging, metadata, fields, genres)
    )

    # Then: reopened MP4 contains exactly the canonical atoms and no source tag.
    tags = MP4(output.output_path).tags
    assert tags is not None
    assert tags['©nam'] == ['Fixture Track']
    assert tags['©ART'] == ['Artist One', 'Artist Two']
    assert tags['aART'] == ['Artist One', 'Artist Two']
    assert tags['©alb'] == ['Fixture Release']
    assert tags['trkn'] == [(1, 2)]
    assert tags['disk'] == [(1, 1)]
    assert tags['©day'] == ['2026-07-28']
    assert tags['©gen'] == ['Rock Rap']
    assert tags['----:com.apple.iTunes:ISRC'] == [b'USFIX2600001']
    assert tags['----:com.apple.iTunes:MusicBrainz Track Id'] == [b'track-id']
    assert '----:com.apple.iTunes:source' not in tags


@pytest.mark.parametrize(
    ('suffix', 'codec', 'reader'),
    (('.ogg', 'vorbis', OggVorbis), ('.opus', 'libopus', OggOpus)),
)
def test_write_canonical_metadata_when_ogg_source_has_observed_comments_reopens_canonical_comments(
    tmp_path: Path, suffix: str, codec: str, reader: type[OggVorbis] | type[OggOpus]
) -> None:
    # Given: an Ogg source with source-observed comments that conflict with canonical facts.
    source = _create_ogg(tmp_path, f'source{suffix}', codec)
    source_tags = reader(source)
    source_tags['TITLE'] = ['Observed title']
    source_tags['SOURCE'] = ['observed']
    source_tags.save()
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()

    # When: canonical metadata is written to a distinct staged Ogg file.
    output = write_canonical_metadata(
        MetadataWriteRequest(source, staging / f'track{suffix}', staging, _metadata(), fields, genres)
    )

    # Then: reopened comments contain the exact canonical contract and no source tags.
    tags = reader(output.output_path)
    assert tags['TITLE'] == ['Fixture Track']
    assert tags['ARTIST'] == ['Artist One; Artist Two']
    assert tags['GENRE'] == ['Hip Hop; Alternative Rock']
    assert tags['MUSICBRAINZ_TRACKID'] == ['track-id']
    assert 'SOURCE' not in tags
    assert 'Observed title' not in tags['TITLE']


@pytest.mark.parametrize('suffix', ('.ogg', '.opus'))
def test_write_canonical_metadata_when_ogg_required_metadata_is_missing_rejects_without_output(
    tmp_path: Path, suffix: str
) -> None:
    # Given: a valid Ogg source and a canonical decision missing its required title.
    source = _create_ogg(tmp_path, f'source{suffix}', 'vorbis' if suffix == '.ogg' else 'libopus')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()

    # When: metadata writing validates the incomplete canonical decision.
    with pytest.raises(MetadataWriteError, match='required'):
        _ = write_canonical_metadata(
            MetadataWriteRequest(
                source, staging / f'track{suffix}', staging, replace(_metadata(), title=''), fields, genres
            )
        )

    # Then: no Ogg output is available for publication.
    assert not (staging / f'track{suffix}').exists()


def test_write_canonical_metadata_when_required_metadata_is_missing_blocks_publication(tmp_path: Path) -> None:
    # Given: a canonical decision with a required title missing.
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    metadata = replace(_metadata(), title='')

    # When: canonical metadata writing validates the decision.
    with pytest.raises(MetadataWriteError, match='required'):
        _ = write_canonical_metadata(
            MetadataWriteRequest(source, staging / 'track.flac', staging, metadata, fields, genres)
        )

    # Then: no output is available for publication or review.
    assert not (staging / 'track.flac').exists()


def test_write_canonical_metadata_when_reviewed_local_only_omits_uninvented_musicbrainz_tags(tmp_path: Path) -> None:
    # Given: a reviewed local-only decision with no MusicBrainz identity.
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()

    # When: the local-only facts are written.
    output = write_canonical_metadata(
        MetadataWriteRequest(
            source, staging / 'track.flac', staging, _metadata(CanonicalSource.REVIEWED_LOCAL_ONLY), fields, genres
        )
    )  # noqa: E501

    # Then: local-only output never fabricates provider IDs.
    listed = dict(read_normalized_tags(output.output_path))
    assert not any(name.startswith('MUSICBRAINZ_') for name in listed)


def test_write_canonical_metadata_when_genre_is_unapproved_rejects_without_output(tmp_path: Path) -> None:
    # Given: a canonical decision carrying an unapproved genre spelling.
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    metadata = replace(_metadata(), genres=('Unknown Genre',))

    # When: metadata writing attempts policy normalization.
    with pytest.raises(MetadataWriteError):
        _ = write_canonical_metadata(
            MetadataWriteRequest(source, staging / 'track.flac', staging, metadata, fields, genres)
        )

    # Then: unknown source-like text cannot become a publish tag.
    assert not (staging / 'track.flac').exists()
