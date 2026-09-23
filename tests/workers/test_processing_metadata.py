from __future__ import annotations

from music_ingest.services.metadata import _fallback_metadata, _publication_layout


def test_fallback_metadata_when_source_is_missing_canonical_tags_returns_no_fallback() -> None:
    # Given: a valid FLAC whose source tags contain only track identity and sequence data.
    tags = (
        ('TITLE', 'Track'),
        ('ARTIST', 'Artist'),
        ('ALBUM', 'Album'),
        ('TRACKNUMBER', '2'),
        ('TRACKTOTAL', '10'),
    )

    # When: the worker attempts to build canonical fallback metadata.
    metadata = _fallback_metadata(tags)

    # Then: missing canonical fields route the source to review instead of inventing values.
    assert metadata is None


def test_publication_layout_when_canonical_metadata_is_available_uses_artist_album_and_track() -> None:
    # Given: canonical tags that identify one track in a multi-disc release.
    tags = (
        ('TITLE', 'Opening / Theme'),
        ('ARTIST', 'Track Artist'),
        ('ALBUM', 'Live Set'),
        ('ALBUMARTIST', 'Album Artist'),
        ('DATE', '2026'),
        ('TRACKNUMBER', '2'),
        ('TRACKTOTAL', '10'),
        ('DISCNUMBER', '2'),
        ('DISCTOTAL', '2'),
        ('GENRE', 'Rock'),
    )

    # When: the worker derives the persistent publication layout.
    layout = _publication_layout(tags, 'source.flac')

    # Then: technical job identifiers are absent from both directory and filename.
    assert layout == ('Album Artist/Live Set', '02-02 - Opening - Theme.mka')


def test_publication_layout_when_source_has_no_tags_uses_unsorted_first_track_name() -> None:
    # Given: a source with no usable tags at all.
    tags: tuple[tuple[str, str], ...] = ()

    # When: the worker derives its fallback publication layout.
    layout = _publication_layout(tags, 'raw.flac')

    # Then: the file gets a stable first-free-style fallback name instead of the source basename.
    assert layout == ('Unsorted', 'Track 01.mka')


def test_publication_layout_when_source_has_partial_album_tags_uses_available_identity() -> None:
    # Given: source tags identify the artist, album, title, and track but omit optional canonical fields.
    tags = (
        ('ARTIST', 'Artist'),
        ('ALBUM', 'Album'),
        ('TITLE', 'Track'),
        ('TRACKNUMBER', '2'),
    )

    # When: the worker derives the persistent publication layout.
    layout = _publication_layout(tags, 'source.flac')

    # Then: available source identity determines the album directory and filename.
    assert layout == ('Artist/Album', '02 - Track.mka')
