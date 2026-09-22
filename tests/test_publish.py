from __future__ import annotations

from dataclasses import replace
from datetime import UTC, datetime
from hashlib import sha256
from os import link
from pathlib import Path
from subprocess import run

import pytest

from music_ingest.models import SourceRecord, SourceRootRecord
from music_ingest.services.normalize.tags import read_normalized_tags, write_normalized_tags
from music_ingest.services.publication.service import (
    PublicationError,
    PublicationRequest,
    publish_release,
    replace_published_audio,
)


def _create_flac(directory: Path, name: str) -> Path:
    path = directory / name
    completed = run(  # noqa: S603
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
    _ = write_normalized_tags(
        path,
        (('ARTIST', 'Artist One; Artist Two'), ('ALBUM', 'Fixture Release'), ('GENRE', 'Hip Hop; Alternative Rock')),
    )
    return path


def _create_audio(directory: Path, name: str, codec: str, container: str) -> Path:
    path = directory / name
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
            codec,
            '-f',
            container,
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    return path


def _request(tmp_path: Path, source: Path, staged_release: Path) -> PublicationRequest:
    media = tmp_path / 'media'
    media.mkdir(exist_ok=True)
    now = datetime.now(UTC)
    source_root = SourceRootRecord(
        id='source-root',
        display_name='source-root',
        canonical_path=str(source.parent.resolve()),
        enabled=True,
        scan_state='scanned',
        created_at=now,
        updated_at=now,
    )
    source_record = SourceRecord(
        id='source',
        source_path=str(source),
        device=source.stat().st_dev,
        inode=source.stat().st_ino,
        size_bytes=source.stat().st_size,
        sha256=sha256(source.read_bytes()).hexdigest(),
        duration_seconds=None,
        origin='manual',
        intake_state='present',
        source_root=source_root,
    )
    return PublicationRequest(
        staged_release=staged_release,
        staging_root=tmp_path / 'staging',
        media_root=media,
        source_paths=(source,),
        sources=(source_record,),
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


@pytest.mark.parametrize(('codec', 'name'), [('alac', '01 - ALAC.m4a'), ('aac', '01 - AAC.m4a')])
def test_publish_release_accepts_registry_declared_m4a_and_preserves_suffix(
    tmp_path: Path, codec: str, name: str
) -> None:
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist' / 'Release'
    release.mkdir(parents=True)
    staged_audio = _create_audio(release, name, codec, 'ipod')
    _ = write_normalized_tags(
        staged_audio,
        (('ARTIST', 'Artist One; Artist Two'), ('ALBUM', 'Fixture Release'), ('GENRE', 'Hip Hop; Alternative Rock')),
    )
    original_bytes = staged_audio.read_bytes()

    result = publish_release(_request(tmp_path, source, release))

    published_audio = result.published_release / staged_audio.name
    assert published_audio.suffix == '.m4a'
    assert published_audio.read_bytes() == original_bytes


def test_publish_release_rejects_raw_aac_before_media_write(tmp_path: Path) -> None:
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist' / 'Release'
    release.mkdir(parents=True)
    _ = _create_audio(release, '01 - Raw.aac', 'aac', 'adts')

    with pytest.raises(PublicationError, match='no declared publication capability|no supported audio'):
        _ = publish_release(_request(tmp_path, source, release))

    assert not (tmp_path / 'media' / 'Artist' / 'Release').exists()


def test_publish_release_rejects_bare_source_paths_without_persisted_root(tmp_path: Path) -> None:
    # Given: a release and source path supplied without a durable source-root ownership record.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist' / 'Release'
    release.mkdir(parents=True)
    (tmp_path / 'media').mkdir()
    _ = _create_flac(release, 'track.flac')

    request = PublicationRequest(
        staged_release=release,
        staging_root=staging,
        media_root=tmp_path / 'media',
        source_paths=(source,),
    )

    # When: a public publication request carries only the bare source path.
    with pytest.raises(PublicationError, match='persisted source'):
        _ = publish_release(request)

    # Then: publication is rejected before any media output is exposed.
    assert not (tmp_path / 'media' / 'Artist' / 'Release').exists()


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


def test_publish_recovery_rejects_requests_without_persisted_source_ownership(tmp_path: Path) -> None:
    # Given: a missing staged release with an existing destination left by an interrupted publication.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    release = staging / 'Artist' / 'Recovered'
    request = replace(_request(tmp_path, source, release), replace_existing=True, sources=())
    (request.media_root / 'Artist' / 'Recovered').mkdir(parents=True)

    # When: recovery receives only a bare source path.
    with pytest.raises(PublicationError, match='persisted source'):
        _ = publish_release(request)

    # Then: it does not accept the existing destination as recovered output.
    assert (request.media_root / 'Artist' / 'Recovered').is_dir()


@pytest.mark.parametrize('root_state', ('disabled', 'historical', 'outside'))
def test_publish_recovery_rejects_invalid_persisted_source_roots(tmp_path: Path, root_state: str) -> None:
    # Given: a missing staged release and an existing destination with an invalid persisted root.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw.flac')
    staging = tmp_path / 'staging'
    staging.mkdir()
    release = staging / 'Artist' / 'Recovered'
    request = replace(_request(tmp_path, source, release), replace_existing=True)
    persisted_source = request.sources[0]
    persisted_root = persisted_source.source_root
    match root_state:
        case 'disabled':
            persisted_root.enabled = False
        case 'historical':
            persisted_root.id = 'historical-unmanaged'
            persisted_root.canonical_path = 'historical-unmanaged://'
        case 'outside':
            outside = tmp_path / 'outside'
            outside.mkdir()
            persisted_root.canonical_path = str(outside)
        case unreachable:
            raise AssertionError(unreachable)
    (request.media_root / 'Artist' / 'Recovered').mkdir(parents=True)

    # When: recovery evaluates the persisted source root before accepting the destination.
    with pytest.raises(PublicationError, match='persisted root boundary'):
        _ = publish_release(request)

    # Then: the pre-existing destination remains untouched and is not accepted as recovery output.
    assert (request.media_root / 'Artist' / 'Recovered').is_dir()


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
    result = publish_release(replace(_request(tmp_path, source, release), destination_release=destination))

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
    _ = publish_release(replace(_request(tmp_path, source, release), destination_release=destination))

    # Then: only one format remains for that track stem.
    assert not (destination / '02 - Second.mp3').exists()
    assert (destination / '02 - Second.flac').exists()


def test_replace_published_audio_when_album_exists_touches_only_target_track_and_artwork(tmp_path: Path) -> None:
    # Given: an existing album and a staged replacement for one track.
    downloads = tmp_path / 'downloads'
    downloads.mkdir()
    source = _create_flac(downloads, 'raw-second.flac')
    staging = tmp_path / 'staging'
    release = staging / 'Artist One' / 'Shared Album'
    release.mkdir(parents=True)
    staged_track = _create_flac(release, '02 - Second.flac')
    replacement_tags = dict(read_normalized_tags(staged_track))
    replacement_tags['TITLE'] = 'Replacement Track'
    _ = write_normalized_tags(staged_track, tuple(replacement_tags.items()))
    (release / 'cover.jpg').write_bytes(b'\xff\xd8\xffnew-cover\xff\xd9')
    destination = tmp_path / 'media' / 'Artist One' / 'Shared Album'
    destination.mkdir(parents=True)
    first_track = _create_flac(destination, '01 - First.flac')
    existing_target = _create_flac(destination, '02 - Second.flac')
    (destination / 'cover.jpg').write_bytes(b'\xff\xd8\xffold-cover\xff\xd9')
    first_hash = sha256(first_track.read_bytes()).hexdigest()
    replacement_hash = sha256(staged_track.read_bytes()).hexdigest()
    existing_target_hash = sha256(existing_target.read_bytes()).hexdigest()

    # When: only the staged track is replaced in the published album.
    result = replace_published_audio(
        replace(_request(tmp_path, source, release), require_canonical_tags=False, replace_artwork=True),
        destination / '02 - Second.flac',
    )

    # Then: unrelated audio remains byte-for-byte unchanged while the target and cover update.
    assert result.published_release == destination
    assert sha256((destination / '01 - First.flac').read_bytes()).hexdigest() == first_hash
    assert sha256((destination / '02 - Second.flac').read_bytes()).hexdigest() == replacement_hash
    assert sha256((destination / '02 - Second.flac').read_bytes()).hexdigest() != existing_target_hash
    assert (destination / 'cover.jpg').read_bytes() == b'\xff\xd8\xffold-cover\xff\xd9'
    assert not release.exists()


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
