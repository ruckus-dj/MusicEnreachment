from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.intake.service import IntakeRequest, Origin, SourceTagObservation, intake_source
from music_ingest.persistence.models import Base


def _client(tmp_path: Path) -> tuple[TestClient, str]:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source_path = tmp_path / 'unknown.flac'
        source_path.write_bytes(b'audio bytes')
        result = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=212,
                tag_observations=(
                    SourceTagObservation(format_name='vorbis', tag_name='ARTIST', value='Шипр'),
                    SourceTagObservation(format_name='vorbis', tag_name='TITLE', value='Белые столбы'),
                ),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        session.commit()
    return TestClient(create_app(lambda: Session(engine))), result.source_id


def test_review_unknown_item_when_queued_exposes_evidence_and_diff(tmp_path: Path) -> None:
    # Given: an unknown source imported without provider candidates.
    client, source_id = _client(tmp_path)

    # When: the operator opens the queue and detail view.
    queue = client.get('/api/review/queue')
    detail = client.get(f'/api/review/items/{source_id}')

    # Then: the item is reviewable and keeps original/proposed evidence visible.
    assert queue.status_code == 200
    assert queue.json()['items'][0]['state'] == 'needs_review'
    assert detail.status_code == 200
    assert detail.json()['original']['artist'] == 'Шипр'
    assert detail.json()['proposed']['release_title'] == 'Белые столбы'
    assert detail.json()['diff'][0]['field'] == 'artist'
    assert detail.json()['fingerprints'] == []
    assert detail.json()['audit'] == []


def test_review_local_only_when_attention_actions_preserve_identity(tmp_path: Path) -> None:
    # Given: a low-confidence unknown source requiring attention without approval.
    client, source_id = _client(tmp_path)

    # When: approval actions are rejected and the operator uses attention actions.
    blocked = client.post(f'/api/review/items/{source_id}/actions/approve', json={})
    approved = client.post(
        f'/api/review/items/{source_id}/actions/edit-and-approve',
        json={'artist': 'Шипр', 'release_title': 'Белые столбы'},
    )
    rematched = client.post(
        f'/api/review/items/{source_id}/actions/attach',
        json={'musicbrainz_id': '11111111-1111-4111-8111-111111111111'},
    )
    rematch = client.post(f'/api/review/items/{source_id}/actions/rematch', json={})
    detail = client.get(f'/api/review/items/{source_id}').json()

    # Then: no approval gate is reachable, local IDs remain stable, and attention actions are audited.
    assert blocked.status_code == 422
    assert approved.status_code == 422
    assert rematched.json()['state'] == 'needs_review'
    assert rematch.json()['state'] == 'needs_review'
    assert detail['ids']['artist'].startswith('local-artist-')
    assert detail['ids']['release'].startswith('local-release-')
    assert [entry['action'] for entry in detail['audit']] == ['attach', 'rematch']
    assert detail['previous_publish_snapshot'] is None


def test_matching_settings_when_updated_are_visible_without_restart(tmp_path: Path) -> None:
    # Given: an API backed by the local persistence schema.
    client, _ = _client(tmp_path)

    # When: the operator reads and then updates the confidence threshold.
    initial = client.get('/api/settings/matching')
    updated = client.put('/api/settings/matching', json={'confidence_threshold': 0.83})
    reread = client.get('/api/settings/matching')

    # Then: the setting is validated and immediately durable through the same app instance.
    assert initial.json() == {'confidence_threshold': 0.7}
    assert updated.status_code == 200
    assert reread.json() == {'confidence_threshold': 0.83}
