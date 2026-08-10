from __future__ import annotations

from dataclasses import replace
from hashlib import sha256
from os import link
from pathlib import Path
from subprocess import run

import pytest

from music_ingest.publication.service import PublicationError, PublicationRequest, publish_release


def _create_flac(directory: Path, name: str) -> Path:
    path = directory / name
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
            str(path),
        ],  # noqa: E501,S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    tagged = run(  # noqa: S603,S607
        [  # noqa: S607
            'metaflac',  # noqa: S607
            '--set-tag=ARTIST=Artist One; Artist Two',
            '--set-tag=ALBUM=Fixture Release',
            '--set-tag=GENRE=Hip Hop; Alternative Rock',
            str(path),
        ],  # noqa: E501,S607
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert tagged.returncode == 0, tagged.stderr
    return path


def _request(tmp_path: Path, source: Path, staged_release: Path) -> PublicationRequest:
    media = tmp_path / 'media'
    media.mkdir()
    return PublicationRequest(
        staged_release=staged_release,
        staging_root=tmp_path / 'staging',
        media_root=media,
        source_paths=(source,),
    )


def test_publish_release_when_complete_exposes_release_without_sidecar_state(tmp_path: Path) -> None:
    # Given: a normalized release under controlled staging and an immutable raw download.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    source_before = source.stat().st_ino, sha256(source.read_bytes()).hexdigest()
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Fixture Release (2026)'
    release.mkdir(parents=True)
    published_track = _create_flac(release, '01 - Fixture Track.flac')
    _ = (release / 'cover.webp').write_bytes(
        b'RIFF\x14\x00\x00\x00WEBPVP8 \n\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00'
    )
    _ = (release / '01 - Fixture Track.lrc').write_text('[00:00.00]Fixture line\n', encoding='utf-8')

    # When: the completed release crosses the publication boundary.
    result = publish_release(_request(tmp_path, source, release))

    # Then: only the completed directory becomes visible, with independent media and no sidecar state.
    assert result.published_release == tmp_path / 'media' / 'Artist One' / 'Fixture Release (2026)'
    assert (result.published_release / published_track.name).exists()
    assert source_before == (source.stat().st_ino, sha256(source.read_bytes()).hexdigest())
    assert (result.published_release / published_track.name).stat().st_ino != source.stat().st_ino
    assert not (tmp_path / 'retention').exists()


def test_publish_release_when_destination_exists_recovers_completed_move(tmp_path: Path) -> None:
    # Given: a validated destination left by an interrupted atomic move and a staged retry.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Recovery Release (2026)'
    release.mkdir(parents=True)
    request = _request(tmp_path, source, release)
    request = replace(request, replace_existing=True)
    destination = request.media_root / 'Artist One' / 'Recovery Release (2026)'
    destination.mkdir(parents=True)
    _ = _create_flac(release, '01 - Recovery.flac')
    _ = _create_flac(destination, '01 - Recovery.flac')

    # When: the retry sees the already moved destination.
    result = publish_release(request)

    # Then: the existing valid destination is returned and the staged retry is removed.
    assert result.published_release == destination
    assert not release.exists()


def test_publish_release_when_album_directory_exists_merges_new_track_without_losing_existing_audio(
    tmp_path: Path,
) -> None:
    # Given: an existing album directory and a staged second track for that album.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw-second.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Shared Album'
    release.mkdir(parents=True)
    _ = _create_flac(release, '02 - Second.flac')
    destination = tmp_path / 'media' / 'Artist One' / 'Shared Album'
    destination.mkdir(parents=True)
    _ = _create_flac(destination, '01 - First.flac')

    # When: the second track is published into the existing album directory.
    result = publish_release(
        PublicationRequest(
            staged_release=release,
            staging_root=staging,
            media_root=tmp_path / 'media',
            source_paths=(source,),
            destination_release=destination,
        )
    )

    # Then: the album directory is reused and both track files remain visible.
    assert result.published_release == destination
    assert {path.name for path in destination.glob('*.flac')} == {'01 - First.flac', '02 - Second.flac'}


def test_publish_release_when_same_track_exists_in_other_audio_format_replaces_that_track_name(
    tmp_path: Path,
) -> None:
    # Given: an existing MP3 target with the same track stem as a staged FLAC.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw-second.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Shared Album'
    release.mkdir(parents=True)
    _ = _create_flac(release, '02 - Second.flac')
    destination = tmp_path / 'media' / 'Artist One' / 'Shared Album'
    destination.mkdir(parents=True)
    (destination / '02 - Second.mp3').write_bytes(b'old-format')

    # When: the FLAC version is published for the same track target.
    _ = publish_release(
        PublicationRequest(
            staged_release=release,
            staging_root=staging,
            media_root=tmp_path / 'media',
            source_paths=(source,),
            destination_release=destination,
        )
    )

    # Then: only one format remains for that track stem.
    assert not (destination / '02 - Second.mp3').exists()
    assert (destination / '02 - Second.flac').exists()


def test_publish_release_when_invalid_lrc_rejects_without_partial_media_visibility(tmp_path: Path) -> None:
    # Given: a staged release containing external text that cannot be decoded as UTF-8.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Invalid Release (2026)'
    release.mkdir(parents=True)
    _ = _create_flac(release, '01 - Invalid.flac')
    _ = (release / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    _ = (release / '01 - Invalid.lrc').write_bytes(b'\xff\xfe')

    # When: publication validates the staged sidecars.
    with pytest.raises(PublicationError, match='UTF-8'):
        _ = publish_release(_request(tmp_path, source, release))

    # Then: the failed release remains staged for quarantine/review and no media directory leaks.
    assert release.exists()
    assert not (tmp_path / 'media' / 'Artist One' / 'Invalid Release (2026)').exists()


def test_publish_release_when_cover_is_missing_publishes_audio_without_media_visibility_gap(tmp_path: Path) -> None:
    # Given: a staged release with canonical audio and no optional external artwork.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    source_before = source.stat().st_ino, sha256(source.read_bytes()).hexdigest()
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Incomplete Release (2026)'
    release.mkdir(parents=True)
    _ = _create_flac(release, '01 - Incomplete.flac')

    # When: publication encounters the release without external artwork.
    result = publish_release(_request(tmp_path, source, release))

    # Then: audio is visible immediately while staging is consumed and source stays intact.
    assert result.published_release == tmp_path / 'media' / 'Artist One' / 'Incomplete Release (2026)'
    assert (result.published_release / '01 - Incomplete.flac').exists()
    assert not release.exists()
    assert source_before == (source.stat().st_ino, sha256(source.read_bytes()).hexdigest())


def test_publish_release_when_staged_audio_hardlinks_download_rejects_media(tmp_path: Path) -> None:
    # Given: a staged track that shares an inode with a raw download.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Linked Release (2026)'
    release.mkdir(parents=True)
    link(source, release / '01 - Linked.flac')
    _ = (release / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')

    # When: release validation examines its published inode boundary.
    with pytest.raises(PublicationError, match='hardlink'):
        _ = publish_release(_request(tmp_path, source, release))

    # Then: downloads remain separate from the media tree.
    assert not (tmp_path / 'media' / 'Artist One' / 'Linked Release (2026)').exists()
