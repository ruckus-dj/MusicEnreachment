from __future__ import annotations

from music_ingest.processing.metadata import _fallback_metadata


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
