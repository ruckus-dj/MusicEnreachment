from __future__ import annotations

from dataclasses import replace
from hashlib import sha256
from pathlib import Path
from subprocess import run

import pytest

from music_ingest.dto import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.metadata import (
    CanonicalMetadata,
    CanonicalSource,
    MetadataWriteError,
    MetadataWriteRequest,
    write_canonical_metadata,
)


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
    listed = run(  # noqa: S603,S607
        ['metaflac', '--list', str(output.output_path)],  # noqa: S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )  # noqa: S603,S607,E501
    assert listed.returncode == 0, listed.stderr
    assert 'comment[0]: TITLE=Fixture Track' in listed.stdout
    assert 'ARTIST=Artist One; Artist Two' in listed.stdout
    assert 'GENRE=Hip Hop; Alternative Rock' in listed.stdout
    assert 'MUSICBRAINZ_ALBUMID=album-id' in listed.stdout
    assert '/' not in next(line for line in listed.stdout.splitlines() if 'GENRE=' in line)
    assert snapshot == (source.stat().st_ino, sha256(source.read_bytes()).hexdigest())


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
    listed = run(  # noqa: S603,S607
        ['metaflac', '--list', str(output.output_path)],  # noqa: S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )  # noqa: S603,S607,E501
    assert listed.returncode == 0, listed.stderr
    assert 'MUSICBRAINZ_' not in listed.stdout


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


def test_write_canonical_metadata_when_tool_claims_success_without_tags_rejects_output(tmp_path: Path) -> None:
    # Given: a local metaflac substitute that returns success while making no metadata change.
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    fake_metaflac = tmp_path / 'fake-metaflac'
    fake_metaflac.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
    fake_metaflac.chmod(0o700)

    # When: the writer receives misleading tool success output.
    with pytest.raises(MetadataWriteError, match='did not produce'):
        _ = write_canonical_metadata(
            MetadataWriteRequest(
                source, staging / 'track.flac', staging, _metadata(), fields, genres, str(fake_metaflac)
            )
        )

    # Then: missing canonical tags block publication.
    assert not (staging / 'track.flac').exists()


def test_write_canonical_metadata_when_metaflac_hangs_cleans_staging(tmp_path: Path) -> None:
    # Given: a local test tool that replaces itself with a bounded sleep.
    source = _create_flac(tmp_path, 'source.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    fields, genres = _policies()
    sleeping_metaflac = tmp_path / 'sleeping-metaflac'
    sleeping_metaflac.write_text('#!/bin/sh\nexec sleep 1\n', encoding='utf-8')
    sleeping_metaflac.chmod(0o700)

    # When: a command exceeds the configured deadline.
    with pytest.raises(MetadataWriteError, match='timed out'):
        _ = write_canonical_metadata(
            MetadataWriteRequest(
                source, staging / 'track.flac', staging, _metadata(), fields, genres, str(sleeping_metaflac), 0.01
            )
        )

    # Then: the timeout leaves no partial output or temporary resource.
    assert tuple(staging.iterdir()) == ()
