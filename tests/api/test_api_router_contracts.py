from __future__ import annotations

import hashlib
import json
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from pathlib import Path
from typing import Never

import pytest
from fastapi import APIRouter, FastAPI
from fastapi.routing import APIRoute
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base, RuntimeSettingRecord
from music_ingest.services.settings import RuntimeSettings


def _unexpected_session() -> Never:
    raise AssertionError('the session factory must not be used while inspecting the application contract')


def _database(tmp_path: Path, name: str, confidence_threshold: float) -> Engine:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / name}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            RuntimeSettingRecord(
                key='matching.confidence_threshold',
                value=str(confidence_threshold),
                updated_at=datetime.now(UTC),
            )
        )
        session.commit()
    return engine


def _runtime_settings_payload(confidence_threshold: float) -> dict[str, object]:
    return {
        'confidence_threshold': confidence_threshold,
        'timeout_seconds': 30,
        'retry_delay_seconds': 10,
        'max_attempts': 3,
        'worker_pools': {},
        'musicbrainz_enabled': True,
        'musicbrainz_user_agent': 'Music Ingest/0.1',
        'musicbrainz_host': 'https://musicbrainz.org',
        'musicbrainz_request_delay_seconds': 1.0,
        'acoustid_enabled': False,
        'acoustid_request_delay_seconds': 0.34,
        'acoustid_client_key': None,
        'artwork_enabled': True,
        'lrclib_enabled': True,
        'lrclib_host': 'https://lrclib.net',
        'lrclib_user_agent': 'Music Ingest/0.1',
        'lrclib_timeout_seconds': 15.0,
        'lrclib_max_attempts': 3,
        'lrclib_request_delay_seconds': 0.3,
        'lrclib_max_response_bytes': 4 * 1024 * 1024,
        'lrclib_match_confidence_threshold': 0.7,
    }


def _api_routes(application: FastAPI) -> list[APIRoute]:
    routes: list[APIRoute] = []
    for registered_route in application.routes:
        if isinstance(registered_route, APIRoute):
            routes.append(registered_route)
        included_router = getattr(registered_route, 'original_router', None)
        if isinstance(included_router, APIRouter):
            routes.extend(route for route in included_router.routes if isinstance(route, APIRoute))
    return routes


def test_create_app_preserves_the_openapi_contract_across_router_decomposition() -> None:
    application = create_app(_unexpected_session)

    canonical_schema = json.dumps(application.openapi(), sort_keys=True, separators=(',', ':')).encode()

    # Baseline captured before app.py was decomposed; router extraction must not alter the public API schema.
    # Refreshed intentionally when LibraryTrackResponse gained the materialized lyrics_status/lyrics_synced fields.
    # Refreshed again when RuntimeSettingsRequest/Response gained the persisted lrclib provider fields.
    # Refreshed when bulk MusicBrainz metadata refresh became a public library endpoint.
    assert hashlib.sha256(canonical_schema).hexdigest() == (
        '09b45e0737cfea223fb5066086253cd7a2c1ee25269aeb5cf6b53502193a4846'
    )


def test_create_app_registers_each_public_method_and_path_once() -> None:
    application = create_app(_unexpected_session)

    route_keys = [(method, route.path) for route in _api_routes(application) for method in sorted(route.methods or ())]

    assert len(route_keys) == 58
    assert len(route_keys) == len(set(route_keys))
    assert all(route.endpoint.__module__.startswith('music_ingest.api.routers.') for route in _api_routes(application))


def test_create_app_instances_isolate_session_factories_and_runtime_callbacks(tmp_path: Path) -> None:
    first_engine = _database(tmp_path, 'first.db', 0.61)
    second_engine = _database(tmp_path, 'second.db', 0.87)
    first_updates: list[RuntimeSettings] = []
    second_updates: list[RuntimeSettings] = []
    try:
        first_app = create_app(lambda: Session(first_engine), on_runtime_settings_updated=first_updates.append)
        second_app = create_app(lambda: Session(second_engine), on_runtime_settings_updated=second_updates.append)
        with TestClient(first_app) as first, TestClient(second_app) as second:
            assert first.get('/api/settings/matching').json() == {'confidence_threshold': 0.61}
            assert second.get('/api/settings/matching').json() == {'confidence_threshold': 0.87}

            response = first.put('/api/settings', json=_runtime_settings_payload(0.73))

            assert response.status_code == 200
            assert [settings.confidence_threshold for settings in first_updates] == [0.73]
            assert response.json()['lrclib_request_delay_seconds'] == 0.3
            assert first.get('/api/settings').json()['lrclib_max_response_bytes'] == 4 * 1024 * 1024
            assert second_updates == []
            assert second.get('/api/settings/matching').json() == {'confidence_threshold': 0.87}
    finally:
        first_engine.dispose()
        second_engine.dispose()


@pytest.mark.parametrize(
    'override',
    (
        {'lrclib_host': 'http://lrclib.net'},
        {'lrclib_host': 'https://lrclib.net/api/get'},
        {'lrclib_user_agent': ''},
        {'lrclib_timeout_seconds': 0},
        {'lrclib_max_attempts': 0},
        {'lrclib_max_attempts': 11},
        {'lrclib_request_delay_seconds': -1},
        {'lrclib_max_response_bytes': 1023},
        {'lrclib_max_response_bytes': 16 * 1024 * 1024 + 1},
    ),
)
def test_runtime_settings_when_lrclib_values_violate_the_contract_are_rejected(
    tmp_path: Path, override: dict[str, object]
) -> None:
    engine = _database(tmp_path, 'lrclib-contract.db', 0.61)
    try:
        application = create_app(lambda: Session(engine))
        with TestClient(application) as client:
            response = client.put('/api/settings', json={**_runtime_settings_payload(0.7), **override})
            settings = client.get('/api/settings').json()

        # Then: the untrusted value never reaches persisted state and the computed defaults stand.
        assert response.status_code == 422
        assert settings['lrclib_host'] == 'https://lrclib.net'
        assert settings['lrclib_max_response_bytes'] == 4 * 1024 * 1024
    finally:
        engine.dispose()


def test_create_app_preserves_the_supplied_lifespan() -> None:
    events: list[str] = []

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        events.append('started')
        yield
        events.append('stopped')

    application = create_app(_unexpected_session, lifespan=lifespan)

    with TestClient(application) as client:
        assert events == ['started']
        assert client.get('/healthz').status_code == 200

    assert events == ['started', 'stopped']


def test_e2e_seed_flag_defaults_to_hidden_and_is_read_dynamically(tmp_path: Path) -> None:
    engine = _database(tmp_path, 'e2e.db', 0.75)
    application = create_app(lambda: Session(engine))
    try:
        with TestClient(application) as client:
            assert client.post('/api/e2e/seed').status_code == 404

            application.state.e2e_seed_enabled = True
            enabled = client.post('/api/e2e/seed')
            assert enabled.status_code == 200
            assert enabled.json()['record_id'] == 'e2e-record'

            application.state.e2e_seed_enabled = False
            assert client.post('/api/e2e/seed').status_code == 404
    finally:
        engine.dispose()
