from __future__ import annotations

from tests.support.navidrome import (
    GenreResponse,
    SearchResponse,
    catalog_matches,
)


def test_catalog_matches_when_each_semicolon_artist_and_genre_is_indexed_independently() -> None:
    # Given: Navidrome responses with individual artist credits and controlled genre values.
    artist_searches = (
        SearchResponse.model_validate({'subsonic-response': {'searchResult3': {'artist': [{'name': 'Artist One'}]}}}),
        SearchResponse.model_validate({'subsonic-response': {'searchResult3': {'artist': [{'name': 'Artist Two'}]}}}),
    )
    genres = GenreResponse.model_validate(
        {'subsonic-response': {'genres': {'genre': [{'value': 'Hip Hop'}, {'value': 'Alternative Rock'}]}}}
    )

    # When: compatibility validation evaluates the indexed catalog structure.
    result = catalog_matches(artist_searches, ('Artist One', 'Artist Two'), genres, ('Hip Hop', 'Alternative Rock'))

    # Then: only independently indexed artists and genres satisfy the contract.
    assert result
