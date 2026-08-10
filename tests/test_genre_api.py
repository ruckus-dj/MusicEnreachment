from __future__ import annotations

import json
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.matching.providers import MusicBrainzHttpResponse
from music_ingest.persistence.models import Base


class GenreTransportFixture:
    def __init__(self, *responses: MusicBrainzHttpResponse) -> None:
        self.responses = list(responses)

    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        return self.responses.pop(0)


def test_genre_sync_when_musicbrainz_catalog_is_available_persists_display_mapping(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "genres.db"}')
    Base.metadata.create_all(engine)
    transport = GenreTransportFixture(
        MusicBrainzHttpResponse(
            status_code=200,
            body=json.dumps(
                {
                    'genre-count': 1,
                    'genre-offset': 0,
                    'genres': [{'id': 'idm-id', 'name': 'idm', 'disambiguation': ''}],
                }
            ).encode(),
        ),
        MusicBrainzHttpResponse(
            status_code=200,
            body=json.dumps({'genre-count': 2, 'genre-offset': 0, 'genres': []}).encode(),
        ),
    )
    client = TestClient(create_app(lambda: Session(engine), genre_transport=transport))

    response = client.post('/api/genres/sync')

    assert response.status_code == 200
    assert response.json()['items'] == [{'musicbrainz_id': 'idm-id', 'source_name': 'idm', 'display_name': 'IDM'}]
    failed_sync = client.post('/api/genres/sync')

    assert failed_sync.status_code == 502
    assert client.get('/api/genres').json()['items'] == [
        {'musicbrainz_id': 'idm-id', 'source_name': 'idm', 'display_name': 'IDM'}
    ]
    assert 'canonical_genres' not in client.get('/api/settings').json()
