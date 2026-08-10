from __future__ import annotations

import json
from dataclasses import dataclass, field

import pytest

from music_ingest.genres import GenreCatalogEntry, GenreCatalogSyncError, sync_genres
from music_ingest.matching.providers import MusicBrainzHttpResponse


@dataclass
class FakeGenreTransport:
    responses: tuple[MusicBrainzHttpResponse, ...]
    calls: list[str] = field(default_factory=list)

    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        self.calls.append(url)
        return self.responses[len(self.calls) - 1]


def test_sync_genres_when_musicbrainz_returns_lowercase_names_creates_display_labels() -> None:
    response = MusicBrainzHttpResponse(
        status_code=200,
        body=json.dumps(
            {
                'genre-count': 3,
                'genre-offset': 0,
                'genres': [
                    {'id': 'idm-id', 'name': 'idm', 'disambiguation': ''},
                    {'id': 'hip-hop-id', 'name': 'hip-hop', 'disambiguation': ''},
                    {'id': '2-tone-id', 'name': '2 tone', 'disambiguation': ''},
                ],
            }
        ).encode(),
    )
    transport = FakeGenreTransport((response,))

    result = sync_genres(transport, user_agent='Music Ingest/0.1 (test@example.com)', sleep=lambda _: None)

    assert result == (
        GenreCatalogEntry('2-tone-id', '2 tone', '2 Tone'),
        GenreCatalogEntry('hip-hop-id', 'hip-hop', 'Hip Hop'),
        GenreCatalogEntry('idm-id', 'idm', 'IDM'),
    )
    assert transport.calls == ['https://musicbrainz.org/ws/2/genre/all?fmt=json&limit=100&offset=0']


@pytest.mark.parametrize(
    'genres',
    (
        [],
        [{'id': 'only-id', 'name': 'only', 'disambiguation': ''}],
    ),
)
def test_sync_genres_when_page_ends_before_reported_count_rejects_partial_catalog(
    genres: list[dict[str, str]],
) -> None:
    response = MusicBrainzHttpResponse(
        status_code=200,
        body=json.dumps({'genre-count': 3, 'genre-offset': 0, 'genres': genres}).encode(),
    )

    with pytest.raises(GenreCatalogSyncError, match='incomplete|truncated|ended'):
        sync_genres(
            FakeGenreTransport((response,)),
            user_agent='Music Ingest/0.1 (test@example.com)',
            sleep=lambda _: None,
        )
