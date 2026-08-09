from __future__ import annotations

from music_ingest.processing.metadata import _fallback_metadata, _publication_layout


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
    assert layout == ('Album Artist/Live Set', '02-02 - Opening - Theme.flac')
