from __future__ import annotations

import pytest
from pydantic import ValidationError

from music_ingest.dto.musicbrainz_genres import GenrePage, GenrePayload


def test_genre_payload_when_given_musicbrainz_field_aliases_parses_id_and_name() -> None:
    # Given: the raw MusicBrainz genre shape, keyed by its API field names.
    payload = GenrePayload.model_validate({'id': 'genre-mbid', 'name': 'dnb'})

    # Then: the payload exposes the aliased fields under their Python names.
    assert payload.musicbrainz_id == 'genre-mbid'
    assert payload.source_name == 'dnb'


@pytest.mark.parametrize('raw', [{'id': '', 'name': 'dnb'}, {'id': 'genre-mbid', 'name': ''}])
def test_genre_payload_when_a_required_field_is_empty_is_rejected(raw: dict[str, str]) -> None:
    with pytest.raises(ValidationError):
        _ = GenrePayload.model_validate(raw)


def test_genre_page_when_given_a_musicbrainz_genre_listing_page_parses_count_offset_and_genres() -> None:
    # Given: a raw MusicBrainz genre listing response page.
    raw = {
        'genre-count': 2,
        'genre-offset': 0,
        'genres': [
            {'id': 'genre-one', 'name': 'dnb'},
            {'id': 'genre-two', 'name': 'idm'},
        ],
    }

    # When: parsing the page.
    page = GenrePage.model_validate(raw)

    # Then: the pagination fields and nested genres are all decoded.
    assert page.count == 2
    assert page.offset == 0
    assert [genre.source_name for genre in page.genres] == ['dnb', 'idm']


@pytest.mark.parametrize('field', ['genre-count', 'genre-offset'])
def test_genre_page_when_pagination_field_is_negative_is_rejected(field: str) -> None:
    raw = {'genre-count': 0, 'genre-offset': 0, 'genres': []}
    raw[field] = -1
    with pytest.raises(ValidationError):
        _ = GenrePage.model_validate(raw)


def test_genre_payload_is_frozen_after_construction() -> None:
    payload = GenrePayload.model_validate({'id': 'genre-mbid', 'name': 'dnb'})
    with pytest.raises(ValidationError):
        payload.source_name = 'idm'
