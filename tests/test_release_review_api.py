from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.persistence.models import (
    AuditRecord,
    CandidateRecord,
    PublicationStateRecord,
    ReleaseFileRecord,
    ReleaseGroupRecord,
    ReleaseRecord,
    SourceRecord,
    SourceTagRecord,
    TagLayerRecord,
    TrackRecord,
)


def _client(tmp_path: Path) -> tuple[TestClient, str]:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "release-review.db"}')
    from music_ingest.persistence.models import Base

    Base.metadata.create_all(engine)
    recorded_at = datetime(2026, 8, 2, tzinfo=UTC)
    with Session(engine) as session:
        source = SourceRecord(
            id='source-fallback',
            source_path='/incoming/Шипр/Белые столбы/01.flac',
            device=1,
            inode=2,
            size_bytes=3,
            sha256='a' * 64,
            duration_seconds=212,
            origin='lidarr',
            intake_state='needs_review',
        )
        group = ReleaseGroupRecord(id='group-fallback', title='Белые столбы')
        release = ReleaseRecord(id='release-fallback', release_group=group, title='Белые столбы')
        track = TrackRecord(id='track-fallback', release=release, position=1, title='Белые столбы')
        release_file = ReleaseFileRecord(
            id='file-fallback',
            track=track,
            source_id=source.id,
            relative_path='Шипр/Белые столбы/01.flac',
            content_sha256='b' * 64,
        )
        original = {'TITLE': 'Белые столбы', 'ARTIST': 'Шипр', 'ALBUM': 'Белые столбы', 'TRACKNUMBER': '1'}
        analyzed = {**original, 'TITLE': 'Белые столбы (candidate)', 'GENRE': 'Hip Hop'}
        final = {**original, 'GENRE': 'Hip Hop'}
        session.add_all(
            (
                source,
                SourceTagRecord(source=source, format_name='vorbis', tag_name='TITLE', value='Белые столбы'),
                SourceTagRecord(source=source, format_name='vorbis', tag_name='ARTIST', value='Шипр'),
                CandidateRecord(
                    source=source,
                    candidate_key='mb-release-1',
                    evidence='{"confidence":0.41,"reason":"ambiguous"}',
                ),
                release_file,
                TagLayerRecord(
                    release_file=release_file,
                    layer='original',
                    revision=1,
                    tags_json=json.dumps(original),
                    recorded_at=recorded_at,
                ),
                TagLayerRecord(
                    release_file=release_file,
                    layer='analyzed',
                    revision=1,
                    tags_json=json.dumps(analyzed),
                    recorded_at=recorded_at,
                ),
                TagLayerRecord(
                    release_file=release_file,
                    layer='final',
                    revision=1,
                    tags_json=json.dumps(final),
                    recorded_at=recorded_at,
                ),
                PublicationStateRecord(release=release, state='published', updated_at=recorded_at),
                AuditRecord(
                    release=release,
                    action='fallback_published',
                    actor='worker',
                    details_json='{"reason":"ambiguous"}',
                    recorded_at=recorded_at,
                ),
            )
        )
        session.commit()
    return TestClient(create_app(lambda: Session(engine))), 'release-fallback'


def test_release_review_when_queued_returns_published_and_attention_releases(tmp_path: Path) -> None:
    # Given: an automatically published fallback release with attention-worthy evidence.
    client, release_id = _client(tmp_path)

    # When: the operator opens the release-level queue.
    response = client.get('/api/release-review/queue')

    # Then: the queue preserves the incoming folder, publication state, and review signal.
    assert response.status_code == 200
    assert response.json()['items'] == [
        {
            'release_id': release_id,
            'incoming_folder': '/incoming/Шипр/Белые столбы',
            'publication_state': 'published',
            'review_state': 'needs_review',
        }
    ]


def test_release_review_when_detail_requested_returns_layers_evidence_and_history(tmp_path: Path) -> None:
    # Given: a published fallback release with original, analyzed, and final tag layers.
    client, release_id = _client(tmp_path)

    # When: the operator inspects the release detail.
    response = client.get(f'/api/release-review/releases/{release_id}')

    # Then: immutable observations and evidence remain separate from final tags and history.
    assert response.status_code == 200
    detail = response.json()
    assert detail['incoming_folder'] == '/incoming/Шипр/Белые столбы'
    assert detail['publication']['state'] == 'published'
    assert detail['tracks'][0]['layers']['original']['ARTIST'] == 'Шипр'
    assert detail['tracks'][0]['layers']['analyzed']['TITLE'] == 'Белые столбы (candidate)'
    assert detail['tracks'][0]['layers']['final']['GENRE'] == 'Hip Hop'
    assert detail['candidates'][0]['confidence'] == 0.41
    assert detail['audit'][0]['action'] == 'fallback_published'


def test_release_review_when_published_fallback_is_edited_creates_revision_and_republishes(tmp_path: Path) -> None:
    # Given: a published fallback release at final revision one.
    client, release_id = _client(tmp_path)

    # When: the operator edits its canonical tags at the current revision.
    response = client.patch(
        f'/api/release-review/releases/{release_id}/tags',
        json={'revision': 1, 'tags': {'TITLE': 'Белые столбы (edited)', 'ARTIST': 'Шипр', 'GENRE': 'Hip Hop'}},
    )
    detail = client.get(f'/api/release-review/releases/{release_id}').json()

    # Then: publication is automatic, source observations are unchanged, and history is append-only.
    assert response.status_code == 200
    assert response.json() == {'release_id': release_id, 'revision': 2, 'publication_state': 'published'}
    assert detail['tracks'][0]['layers']['original']['TITLE'] == 'Белые столбы'
    assert detail['tracks'][0]['layers']['final']['TITLE'] == 'Белые столбы (edited)'
    assert detail['tracks'][0]['final_revision'] == 2
    assert [entry['action'] for entry in detail['audit']] == ['fallback_published', 'edited', 'republished']


def test_release_review_when_stale_or_invalid_edit_rejects_without_overwrite(tmp_path: Path) -> None:
    # Given: a release with revision one.
    client, release_id = _client(tmp_path)

    # When: the operator submits a stale edit and an invalid canonical field.
    stale = client.patch(
        f'/api/release-review/releases/{release_id}/tags',
        json={'revision': 2, 'tags': {'TITLE': 'stale', 'ARTIST': 'Шипр', 'GENRE': 'Hip Hop'}},
    )
    invalid = client.patch(
        f'/api/release-review/releases/{release_id}/tags',
        json={'revision': 1, 'tags': {'TITLE': 'invalid', 'ARTIST': 'Шипр', 'COMMENT': 'source noise'}},
    )
    detail = client.get(f'/api/release-review/releases/{release_id}').json()

    # Then: optimistic concurrency and canonical field validation leave published tags intact.
    assert stale.status_code == 409
    assert invalid.status_code == 422
    assert detail['tracks'][0]['layers']['final']['TITLE'] == 'Белые столбы'
    assert detail['tracks'][0]['final_revision'] == 1


def test_release_review_when_republished_or_rolled_back_appends_revisions(tmp_path: Path) -> None:
    # Given: a published fallback release with an edited second revision.
    client, release_id = _client(tmp_path)
    edited = client.patch(
        f'/api/release-review/releases/{release_id}/tags',
        json={'revision': 1, 'tags': {'TITLE': 'Белые столбы (edited)', 'ARTIST': 'Шипр', 'GENRE': 'Hip Hop'}},
    )
    assert edited.status_code == 200

    # When: the operator republishes and then rolls back to the saved first revision.
    republished = client.post(f'/api/release-review/releases/{release_id}/republish', json={'revision': 2})
    rolled_back = client.post(f'/api/release-review/releases/{release_id}/rollback', json={'revision': 1})
    detail = client.get(f'/api/release-review/releases/{release_id}').json()

    # Then: both actions remain approval-free, publish automatically, and rollback is a new immutable revision.
    assert republished.status_code == 200
    assert rolled_back.status_code == 200
    assert rolled_back.json()['revision'] == 3
    assert detail['tracks'][0]['layers']['final']['TITLE'] == 'Белые столбы'
    assert detail['tracks'][0]['final_revision'] == 3
    assert [entry['action'] for entry in detail['audit']] == [
        'fallback_published',
        'edited',
        'republished',
        'republished',
        'rolled_back',
        'republished',
    ]


def test_release_review_when_missing_or_malformed_action_returns_client_error(tmp_path: Path) -> None:
    # Given: a local review API with no requested release.
    client, _ = _client(tmp_path)

    # When: the browser sends a missing release request or malformed republish action.
    missing = client.get('/api/release-review/releases/missing')
    malformed = client.post('/api/release-review/releases/release-fallback/republish', json={'revision': 'bad'})

    # Then: the API reports a client error without creating review state.
    assert missing.status_code == 404
    assert malformed.status_code == 422
