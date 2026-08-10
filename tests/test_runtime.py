from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import anyio
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

import music_ingest.api.server as server
from music_ingest import __main__ as command
from music_ingest.api.app import create_app
from music_ingest.api.server import RuntimeConfig, RuntimeConfigurationError
from music_ingest.models import Base, JobRecord, RuntimeSettingRecord


def test_entrypoint_when_dry_run_is_requested_keeps_the_dry_run_command(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # Given: a source directory and report directory passed through the module entrypoint.
    source_directory = tmp_path / 'library'
    report_directory = tmp_path / 'reports'
    source_directory.mkdir()
    _ = (source_directory / 'sample.lrc').write_text('[00:00.00]lyrics', encoding='utf-8')
    monkeypatch.setattr(command, 'argv', ['music-ingest', 'dry-run', str(source_directory), str(report_directory)])

    # When: the established dry-run CLI is executed.
    command.main()

    # Then: it emits report locations and remains independent from the service runtime.
    assert 'dry-run reports:' in capsys.readouterr().out
    assert (report_directory / 'summary.json').exists()


def test_runtime_health_when_review_app_is_created_returns_service_status() -> None:
    # Given: a review application with a session factory that is not used by health checks.
    engine = create_engine('sqlite+pysqlite:///:memory:')
    client = TestClient(create_app(lambda: Session(engine)))

    # When: an orchestration health probe reaches the ASGI service.
    response = client.get('/healthz')

    # Then: the probe receives the stable service status without a database query.
    assert response.status_code == 200
    assert response.json() == {'status': 'ok', 'service': 'music-ingest'}


def test_settings_when_existing_acoustid_key_and_blank_update_preserves_key(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "settings.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            RuntimeSettingRecord(
                key='providers.acoustid.client_key', value='stored-secret', updated_at=datetime.now(UTC)
            )
        )
        session.commit()
    client = TestClient(create_app(lambda: Session(engine)))

    response = client.put(
        '/api/settings',
        json={
            'confidence_threshold': 0.8,
            'timeout_seconds': 30,
            'retry_delay_seconds': 10,
            'max_attempts': 3,
            'musicbrainz_enabled': True,
            'musicbrainz_user_agent': 'Music Ingest/0.1',
            'acoustid_enabled': True,
            'acoustid_client_key': None,
            'artwork_enabled': True,
        },
    )

    assert response.status_code == 200
    assert response.json()['acoustid_client_key_configured'] is True


@pytest.mark.parametrize(
    'environment',
    (
        {},
        {'MUSIC_INGEST_DATABASE_URL': 'sqlite+pysqlite:///:memory:'},
        {
            'MUSIC_INGEST_DATABASE_URL': 'postgresql+psycopg://music_ingest@database/music_ingest',
            'MUSIC_INGEST_DATABASE_CONNECT_TIMEOUT_SECONDS': '0',
        },
    ),
)
def test_runtime_configuration_when_database_settings_are_invalid_fails_closed(environment: dict[str, str]) -> None:
    # Given: a missing, SQLite, or non-positive-timeout runtime configuration.
    # When: the service parses its required database settings.
    # Then: it refuses to start rather than changing storage backends or hanging indefinitely.
    with pytest.raises(RuntimeConfigurationError):
        _ = RuntimeConfig.from_environment(environment)


def test_runtime_app_when_started_upgrades_schema_before_it_serves_requests(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a configured PostgreSQL runtime with migration and engine seams isolated from external services.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    migrations: list[RuntimeConfig] = []
    engine = create_engine('sqlite+pysqlite:///:memory:')
    monkeypatch.setattr(server, 'run_migrations', migrations.append)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)

    # When: Uvicorn constructs the application factory.
    client = TestClient(server.create_runtime_app())
    response = client.get('/healthz')

    # Then: Alembic migration is completed before the ready health response is exposed.
    assert migrations == [runtime_config]
    assert response.status_code == 200


def test_runtime_app_when_shutdown_disposes_its_engine(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a runtime engine with a recorded disposal boundary.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime.db"}')
    Base.metadata.create_all(engine)
    disposed: list[None] = []
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    monkeypatch.setattr(engine, 'dispose', lambda: disposed.append(None))

    # When: Uvicorn's ASGI lifespan enters and exits through TestClient.
    with TestClient(server.create_runtime_app()) as client:
        response = client.get('/healthz')

    # Then: readiness is served during the lifespan and its database pool is disposed on shutdown.
    assert response.status_code == 200
    assert disposed == [None]


async def _record_worker_start(*_args: object) -> None:
    await anyio.sleep_forever()


def test_runtime_app_when_started_runs_the_processing_worker(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a runtime with an isolated processing-worker boundary.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime.db"}')
    Base.metadata.create_all(engine)
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    started: list[tuple[object, object]] = []

    async def record_worker_start(session_factory: object, config: object) -> None:
        started.append((session_factory, config))
        await _record_worker_start()

    monkeypatch.setattr(server, 'run_processing_worker', record_worker_start)

    # When: the ASGI lifespan starts.
    with TestClient(server.create_runtime_app()):
        pass

    # Then: the DB-backed worker runs alongside the web runtime.
    assert len(started) == 1


def test_runtime_app_when_non_default_incoming_root_receives_download_uses_the_configured_root(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a configured runtime and an incoming file outside the application's default root.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime.db"}')
    Base.metadata.create_all(engine)
    incoming_root = tmp_path / 'configured-incoming'
    incoming_root.mkdir()
    source_path = incoming_root / 'fixture.flac'
    _ = source_path.write_bytes(b'not a FLAC container')
    monkeypatch.setenv('MUSIC_INGEST_INCOMING_ROOT', str(incoming_root))
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    monkeypatch.setattr(server, 'run_processing_worker', _record_worker_start)

    # When: the runtime-owned HTTP app receives a Download webhook.
    with TestClient(server.create_runtime_app()) as client:
        response = client.post(
            '/api/intake/lidarr',
            json={'eventType': 'Download', 'trackFiles': [{'path': str(source_path)}], 'isUpgrade': False},
        )

    # Then: the configured root is accepted and creates source-linked durable work.
    assert response.status_code == 202
    with Session(engine) as session:
        job = session.scalars(select(JobRecord)).one()
        assert job.source_id is not None


def test_migrations_when_runtime_starts_upgrades_to_head(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a configured PostgreSQL URL and a recorded Alembic command boundary.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 7)
    captured: list[tuple[str, str, str]] = []

    def upgrade(config, revision: str) -> None:
        captured.append(
            (
                config.get_main_option('sqlalchemy.url'),
                config.get_main_option('music_ingest.connect_timeout_seconds'),
                revision,
            )
        )

    monkeypatch.setattr(server.command, 'upgrade', upgrade)

    # When: the runtime executes its migration boundary.
    server.run_migrations(runtime_config)

    # Then: an outdated database is explicitly upgraded to Alembic head with bounded connection setup.
    assert captured == [(runtime_config.database_url, '7', 'head')]
