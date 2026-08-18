from __future__ import annotations

from pathlib import Path

import pytest

from music_ingest.enrichment.artwork import (
    ArtworkCandidate,
    ArtworkFormat,
    ArtworkWriteError,
    ArtworkWriteRequest,
    ManagedArtworkWriteRequest,
    write_managed_release_artwork,
    write_release_artwork,
)

WEBP = b'RIFF\x14\x00\x00\x00WEBPVP8 \n\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00'


def test_write_release_artwork_when_verified_release_image_writes_exactly_one_external_cover(tmp_path: Path) -> None:
    # Given: a release-linked WebP fixture and a release directory inside staging.
    staging = tmp_path / 'staging'
    release = staging / 'Fixture Release'
    release.mkdir(parents=True)
    candidate = ArtworkCandidate('release-id', ArtworkFormat.WEBP, WEBP)

    # When: the verified candidate is staged for that release.
    output = write_release_artwork(ArtworkWriteRequest(staging, release, 'release-id', candidate))

    # Then: one external sidecar exists and no alternate cover is emitted.
    assert output == release / 'cover.webp'
    assert output.read_bytes() == WEBP
    assert tuple(path.name for path in release.iterdir()) == ('cover.webp',)


def test_write_release_artwork_when_untrusted_or_wrong_release_rejects_without_cover(tmp_path: Path) -> None:
    # Given: a malformed candidate whose claimed release does not match the selected release.
    staging = tmp_path / 'staging'
    release = staging / 'Fixture Release'
    release.mkdir(parents=True)
    candidate = ArtworkCandidate('other-release', ArtworkFormat.WEBP, b'untrusted image bytes')

    # When: artwork crosses the release-art boundary.
    with pytest.raises(ArtworkWriteError):
        _ = write_release_artwork(ArtworkWriteRequest(staging, release, 'release-id', candidate))

    # Then: artwork cannot publish without verified release identity and a valid image envelope.
    assert tuple(release.iterdir()) == ()


def test_write_release_artwork_when_stale_cover_exists_rejects_second_sidecar(tmp_path: Path) -> None:
    # Given: an already accepted external cover in a staged release.
    staging = tmp_path / 'staging'
    release = staging / 'Fixture Release'
    release.mkdir(parents=True)
    candidate = ArtworkCandidate('release-id', ArtworkFormat.WEBP, WEBP)
    request = ArtworkWriteRequest(staging, release, 'release-id', candidate)
    _ = write_release_artwork(request)

    # When: stale state attempts to add a second release cover.
    with pytest.raises(ArtworkWriteError, match='already has artwork'):
        _ = write_release_artwork(request)

    # Then: exactly one external artwork sidecar remains.
    assert tuple(path.name for path in release.iterdir()) == ('cover.webp',)


def test_write_managed_release_artwork_when_release_is_under_media_root_writes_sidecar(tmp_path: Path) -> None:
    # Given: a managed album directory below the configured media root.
    media_root = tmp_path / 'media'
    release = media_root / 'Artist' / 'Album [release-i]'
    release.mkdir(parents=True)
    candidate = ArtworkCandidate('release-id', ArtworkFormat.WEBP, WEBP)

    # When: the verified release artwork is enriched after publication.
    output = write_managed_release_artwork(ManagedArtworkWriteRequest(media_root, release, 'release-id', candidate))

    # Then: the managed album receives exactly one cover sidecar.
    assert output == release / 'cover.webp'
    assert output.read_bytes() == WEBP
