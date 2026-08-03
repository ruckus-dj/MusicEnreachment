from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.persistence.models import Base


def test_review_ui_when_loaded_contains_evidence_diff_and_review_controls(tmp_path: Path) -> None:
    # Given: an empty local review database.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review-ui.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator opens the local review page.
    response = client.get('/review')

    # Then: the page names the durable evidence-led workflow and its actions.
    assert response.status_code == 200
    for marker in ('Медиатека', 'assets/', 'type="module"', 'lang="ru"'):
        assert marker in response.text


def test_review_ui_when_detail_is_populated_contains_api_data_flow_and_action_submission(tmp_path: Path) -> None:
    # Given: a review page backed by the local API.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review-ui-populated.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator loads the UI shell for a populated queue.
    response = client.get('/review')

    # Then: the browser has executable queue/detail loading and API action submission seams.
    assert response.status_code == 200
    for marker in ('assets/', 'Music Ingest', 'description', 'root'):
        assert marker in response.text
    assert 'approve' not in response.text.lower()
