from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base


def _client(tmp_path: Path) -> tuple[TestClient, Path]:
    source_parent = tmp_path / 'sources'
    source_parent.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "roots.db"}')
    Base.metadata.create_all(engine)
    return (
        TestClient(create_app(lambda: Session(engine), source_roots_parent=source_parent)),
        source_parent,
    )


def test_source_roots_when_requests_are_unauthenticated_create_update_disable_and_restart_are_durable(
    tmp_path: Path,
) -> None:
    # Given: a mounted source parent and a selectable immediate child.
    client, source_parent = _client(tmp_path)
    archive = source_parent / 'archive'
    archive.mkdir()

    # When: an operator configures, renames, and disables that root.
    initial = client.get('/api/settings/source-roots')
    created = client.post('/api/settings/source-roots', json={'path': str(archive), 'display_name': 'Archive'})
    root_id = created.json()['id']
    updated = client.put(
        f'/api/settings/source-roots/{root_id}',
        json={'display_name': 'Cold archive', 'enabled': False},
    )
    restarted = client.get('/api/settings/source-roots')

    # Then: the configured root survives a fresh request with its durable state.
    assert initial.status_code == 200
    assert initial.json()['items'] == []
    assert created.status_code == 201
    assert updated.status_code == 200
    assert [(root['id'], root['display_name'], root['enabled']) for root in restarted.json()['items']] == [
        (root_id, 'Cold archive', False)
    ]


def test_source_roots_when_unsafe_reject_without_mutating_state(tmp_path: Path) -> None:
    # Given: a mounted source parent containing safe, nested, external, and symlink candidates.
    client, source_parent = _client(tmp_path)
    safe = source_parent / 'safe'
    nested = safe / 'nested'
    outside = tmp_path / 'outside'
    safe.mkdir()
    nested.mkdir()
    outside.mkdir()
    symlink = source_parent / 'linked'
    symlink.symlink_to(outside, target_is_directory=True)

    # When: callers submit paths that cannot be mounted safely.
    responses = [
        client.post('/api/settings/source-roots', json={'path': candidate, 'display_name': 'bad'})
        for candidate in ('..', str(source_parent), str(outside), str(symlink), str(nested), str(tmp_path / 'missing'))
    ]
    accepted = client.post('/api/settings/source-roots', json={'path': str(safe), 'display_name': 'safe'})
    duplicate = client.post('/api/settings/source-roots', json={'path': str(safe), 'display_name': 'again'})
    listed = client.get('/api/settings/source-roots')

    # Then: only the canonical immediate child is persisted; unsafe inputs cannot add host access.
    assert all(response.status_code == 422 for response in responses)
    assert accepted.status_code == 201
    assert duplicate.status_code == 409
    assert [root['canonical_path'] for root in listed.json()['items']] == [str(safe)]


def test_source_roots_when_operator_uses_picker_can_list_and_remove_an_unused_root(tmp_path: Path) -> None:
    # Given: a mounted parent with an existing safe directory and a configured root with no observations.
    client, source_parent = _client(tmp_path)
    archive = source_parent / 'archive'
    archive.mkdir()
    created = client.post('/api/settings/source-roots', json={'path': str(archive), 'display_name': 'Archive'})

    # When: the operator loads the picker and removes the configured root.
    candidates = client.get('/api/settings/source-roots/candidates')
    removed = client.delete(f'/api/settings/source-roots/{created.json()["id"]}')
    listed = client.get('/api/settings/source-roots')

    # Then: the picker exposes only the mounted immediate children and removal changes configuration, not the folder.
    assert candidates.status_code == 200
    assert candidates.json()['items'] == [
        {'name': 'archive', 'canonical_path': str(archive)},
    ]
    assert removed.status_code == 204
    assert archive.is_dir()
    assert listed.json()['items'] == []


def test_source_roots_when_root_has_observations_archives_them_on_removal(tmp_path: Path) -> None:
    # Given: an E2E fixture root with a persisted source observation.
    source_parent = tmp_path / 'sources'
    source_parent.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "roots-enabled.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(
        create_app(
            lambda: Session(engine),
            source_roots_parent=source_parent,
            e2e_seed_enabled=True,
        )
    )
    seeded = client.post('/api/e2e/seed')

    # When: an operator tries to remove the root that owns the observation.
    removed = client.delete('/api/settings/source-roots/e2e')

    # Then: the historical evidence remains intact and the configured root is gone.
    assert seeded.status_code == 200
    assert removed.status_code == 204
    assert client.get('/api/settings/source-roots').json()['items'] == []


def test_full_reprocess_when_historical_sources_exist_skips_them(tmp_path: Path) -> None:
    # Given: an archived source root that retains its historical observations.
    source_parent = tmp_path / 'sources'
    source_parent.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reprocess.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(
        create_app(
            lambda: Session(engine),
            source_roots_parent=source_parent,
            e2e_seed_enabled=True,
        )
    )
    assert client.post('/api/e2e/seed').status_code == 200
    assert client.delete('/api/settings/source-roots/e2e').status_code == 204

    # When: the operator requests a full reprocess.
    response = client.post('/api/library/reprocess-all')

    # Then: unavailable historical observations do not abort active-library processing.
    assert response.status_code == 200
    assert response.json() == {'queued': 0}
    source_reprocess = client.post('/api/library/records/e2e-record/sources/e2e-source-a/reprocess')
    assert source_reprocess.status_code == 404


def test_review_routes_when_application_has_no_token_are_public(tmp_path: Path) -> None:
    # Given: an application with no application-owned authentication.
    client, _ = _client(tmp_path)

    # When: an unauthenticated probe reaches health and a UI route.
    health = client.get('/healthz')
    review = client.get('/review')

    # Then: the health and UI routes are both available without credentials.
    assert health.status_code == 200
    assert review.status_code == 200


def test_e2e_seed_when_disabled_is_not_available(tmp_path: Path) -> None:
    # Given: a normal application without the explicit E2E seed flag.
    client, _ = _client(tmp_path)

    # When: a caller requests disposable fixtures.
    response = client.post('/api/e2e/seed')

    # Then: the seed capability is unavailable.
    assert response.status_code == 404


def test_e2e_seed_when_enabled_creates_fixed_fixtures(tmp_path: Path) -> None:
    # Given: an application with the explicit E2E seed flag.
    client, _ = _client(tmp_path)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "roots-enabled.db"}')
    Base.metadata.create_all(engine)
    application = create_app(lambda: Session(engine))
    client = TestClient(application)
    application.state.e2e_seed_enabled = True

    # When: the E2E setup requests disposable fixtures.
    response = client.post('/api/e2e/seed')

    # Then: fixed record/source identifiers and valid provider evidence are returned.
    assert response.status_code == 200
    assert response.json() == {
        'record_id': 'e2e-record',
        'source_ids': ['e2e-source-a', 'e2e-source-b'],
        'recording_mbid': 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
        'correction_mbid': '11111111-1111-4111-8111-111111111111',
    }
