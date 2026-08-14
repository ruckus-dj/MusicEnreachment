from __future__ import annotations

import os
from collections.abc import Callable
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
from music_ingest.models import Base, JobRecord, RuntimeSettingRecord, SourceRootRecord
from music_ingest.processing import ProcessingConfig
from music_ingest.processing import runtime as processing_runtime


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
            'worker_concurrency': 4,
            'musicbrainz_enabled': True,
            'musicbrainz_user_agent': 'Music Ingest/0.1',
            'musicbrainz_host': 'https://musicbrainz.internal',
            'musicbrainz_request_delay_seconds': 0,
            'acoustid_enabled': True,
            'acoustid_request_delay_seconds': 0.5,
            'acoustid_client_key': None,
            'artwork_enabled': True,
        },
    )

    assert response.status_code == 200
    assert response.json()['acoustid_client_key_configured'] is True
    assert response.json()['musicbrainz_host'] == 'https://musicbrainz.internal'
    assert response.json()['musicbrainz_request_delay_seconds'] == 0
    assert response.json()['acoustid_request_delay_seconds'] == 0.5
    assert response.json()['worker_concurrency'] == 4


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


def test_runtime_configuration_when_api_token_is_missing_starts_without_application_auth() -> None:
    # Given: an otherwise valid PostgreSQL runtime configuration without application credentials.
    environment = {'MUSIC_INGEST_DATABASE_URL': 'postgresql+psycopg://music_ingest@database/music_ingest'}

    # When: the service parses startup configuration.
    # Then: reverse-proxy ownership leaves the application configuration token-free.
    assert RuntimeConfig.from_environment(environment) == RuntimeConfig(
        'postgresql+psycopg://music_ingest@database/music_ingest', 10
    )


def test_runtime_app_when_e2e_seed_flag_is_explicitly_enabled_sets_seed_state(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: a valid runtime with the explicit E2E seed flag enabled.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine('sqlite+pysqlite:///:memory:')
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    monkeypatch.setenv('MUSIC_INGEST_E2E_SEED_ENABLED', 'true')

    # When: the runtime application is composed.
    application = server.create_runtime_app()

    # Then: the explicit flag reaches the application state.
    assert application.state.e2e_seed_enabled is True


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


def test_runtime_app_passes_live_transport_to_musicbrainz_review_endpoint(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: the runtime creates a live provider transport.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    transport = object()
    captured: dict[str, object] = {}
    engine = create_engine('sqlite+pysqlite:///:memory:')
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    monkeypatch.setattr(server, 'build_live_transport', lambda **_kwargs: transport)

    def capture_app(*_args: object, **kwargs: object):
        captured.update(kwargs)
        return server.FastAPI()

    monkeypatch.setattr(server, 'create_app', capture_app)

    # When: the runtime application is constructed.
    _ = server.create_runtime_app()

    # Then: the API receives the same transport used by the worker.
    assert captured['musicbrainz_transport'] is transport


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
    source_parent = tmp_path / 'sources'
    incoming_root = source_parent / 'legacy'
    incoming_root.mkdir(parents=True)
    monkeypatch.setenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', str(source_parent))

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
    source_parent = tmp_path / 'sources'
    incoming_root = source_parent / 'legacy'
    incoming_root.mkdir(parents=True)
    monkeypatch.setenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', str(source_parent))
    started: list[tuple[object, object, object, object]] = []

    async def record_worker_start(
        session_factory: object, config: object, poll_seconds: object, monitor: object
    ) -> None:
        started.append((session_factory, config, poll_seconds, monitor))
        await _record_worker_start()

    monkeypatch.setattr(server, 'run_processing_worker', record_worker_start)

    # When: the ASGI lifespan starts.
    with TestClient(server.create_runtime_app()):
        pass

    # Then: the DB-backed worker runs alongside the web runtime.
    assert len(started) == 1


def test_processing_runtime_when_started_supervises_all_configurable_worker_slots(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: the processing supervisor with its worker-slot boundary replaced by a cancellable observer.
    started: list[int] = []

    async def record_worker_slot(
        _session_factory: Callable[[], Session], _config: ProcessingConfig, worker_slot: int, _poll_seconds: float
    ) -> None:
        started.append(worker_slot)
        await anyio.sleep_forever()

    monkeypatch.setattr(processing_runtime, '_run_processing_worker_slot', record_worker_slot)

    async def start_then_cancel() -> None:
        with anyio.move_on_after(0.1):
            await processing_runtime.run_processing_worker(
                lambda: Session(), ProcessingConfig(Path('/incoming'), Path('/staging'), Path('/media'))
            )

    # When: the supervisor starts.
    anyio.run(start_then_cancel)

    # Then: it creates every bounded slot so the live setting can activate up to the validated maximum.
    assert started == list(range(8))


def test_runtime_app_when_configured_source_root_receives_download_uses_that_root(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a configured runtime and an incoming file below a durable source root.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime.db"}')
    Base.metadata.create_all(engine)
    source_parent = tmp_path / 'sources'
    incoming_root = source_parent / 'incoming'
    incoming_root.mkdir(parents=True)
    source_path = incoming_root / 'fixture.flac'
    _ = source_path.write_bytes(b'not a FLAC container')
    with Session(engine) as session:
        now = datetime.now(UTC)
        session.add(
            SourceRootRecord(
                id='root-incoming',
                display_name='Incoming',
                canonical_path=str(incoming_root.resolve()),
                enabled=True,
                scan_state='never_scanned',
                created_at=now,
                updated_at=now,
            )
        )
        session.commit()
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

    # Then: the configured source root is accepted and creates source-linked durable work.
    assert response.status_code == 202
    with Session(engine) as session:
        job = session.scalars(select(JobRecord)).one()
        assert job.source_id is not None


def test_migrations_when_runtime_starts_upgrades_to_head(monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a configured PostgreSQL URL without configured source paths.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 7)
    captured: list[tuple[str, str, str]] = []

    def upgrade(config, revision: str) -> None:
        bootstrap_root = Path(config.cmd_opts.x[0].split('=', 1)[1])
        captured.append(
            (
                config.get_main_option('sqlalchemy.url'),
                config.get_main_option('music_ingest.connect_timeout_seconds'),
                revision,
            )
        )
        assert bootstrap_root.is_dir()
        assert bootstrap_root.parent == Path(os.environ['MUSIC_INGEST_SOURCE_ROOTS_PARENT'])

    monkeypatch.setattr(server.command, 'upgrade', upgrade)
    monkeypatch.delenv('MUSIC_INGEST_INCOMING_ROOT', raising=False)
    monkeypatch.delenv('MUSIC_INGEST_SOURCE_ROOTS_PARENT', raising=False)

    # When: the runtime executes its migration boundary.
    server.run_migrations(runtime_config)

    # Then: an outdated database reaches head using an isolated migration bootstrap.
    assert captured == [(runtime_config.database_url, '7', 'head')]
    assert 'MUSIC_INGEST_SOURCE_ROOTS_PARENT' not in os.environ
