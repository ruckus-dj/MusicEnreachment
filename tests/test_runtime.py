from __future__ import annotations

from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path

import anyio
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event, select
from sqlalchemy.orm import Session

import music_ingest.bootstrap.server as server
import music_ingest.workers.runtime as processing_runtime
from music_ingest.adapters.external.lrclib import (
    DEFAULT_USER_AGENT,
    MAX_RESPONSE_BODY_BYTES,
    LrclibLookupRequest,
    lookup_url,
)
from music_ingest.api.app import create_app
from music_ingest.bootstrap.server import RuntimeConfig, RuntimeConfigurationError
from music_ingest.contracts import RuntimeSettings
from music_ingest.models import Base, JobRecord, RuntimeSettingRecord, SourceRootRecord
from music_ingest.services.settings import (
    SettingKey,
    build_runtime_settings,
    get_setting_value,
    get_setting_values,
    save_runtime_settings,
)
from music_ingest.workers.config import ProcessingConfig


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
    updates: list[RuntimeSettings] = []
    client = TestClient(create_app(lambda: Session(engine), on_runtime_settings_updated=updates.append))

    response = client.put(
        '/api/settings',
        json={
            'confidence_threshold': 0.8,
            'timeout_seconds': 30,
            'retry_delay_seconds': 10,
            'max_attempts': 3,
            'worker_pools': {'filesystem_scan': 7, 'musicbrainz_analysis': 3},
            'musicbrainz_enabled': True,
            'musicbrainz_user_agent': 'Music Ingest/0.1',
            'musicbrainz_host': 'https://musicbrainz.internal',
            'musicbrainz_request_delay_seconds': 0,
            'acoustid_enabled': True,
            'acoustid_request_delay_seconds': 0.5,
            'acoustid_client_key': None,
            'artwork_enabled': True,
            'lrclib_enabled': False,
            'lrclib_host': 'https://lrclib.internal',
            'lrclib_user_agent': 'Music Ingest/0.1 (ops@example.com)',
            'lrclib_timeout_seconds': 20,
            'lrclib_max_attempts': 5,
            'lrclib_request_delay_seconds': 0.75,
            'lrclib_max_response_bytes': 2 * 1024 * 1024,
            'lrclib_match_confidence_threshold': 0.7,
        },
    )

    assert response.status_code == 200
    assert response.json()['acoustid_client_key_configured'] is True
    assert response.json()['musicbrainz_host'] == 'https://musicbrainz.internal'
    assert response.json()['musicbrainz_request_delay_seconds'] == 0
    assert response.json()['acoustid_request_delay_seconds'] == 0.5
    assert response.json()['worker_pools']['filesystem_scan'] == 7
    assert response.json()['worker_pools']['musicbrainz_analysis'] == 3
    assert client.get('/api/settings').json()['worker_pools'] == response.json()['worker_pools']
    assert response.json()['lrclib_enabled'] is False
    assert response.json()['lrclib_host'] == 'https://lrclib.internal'
    assert response.json()['lrclib_user_agent'] == 'Music Ingest/0.1 (ops@example.com)'
    assert response.json()['lrclib_timeout_seconds'] == 20
    assert response.json()['lrclib_max_attempts'] == 5
    assert response.json()['lrclib_request_delay_seconds'] == 0.75
    assert response.json()['lrclib_max_response_bytes'] == 2 * 1024 * 1024
    assert response.json()['lrclib_match_confidence_threshold'] == 0.7
    assert updates[-1].musicbrainz_request_delay_seconds == 0
    assert updates[-1].lrclib_max_attempts == 5


def test_settings_when_lrclib_values_are_saved_round_trip_through_the_persisted_keys(tmp_path: Path) -> None:
    # Given: an empty settings database.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lrclib-settings.db"}')
    Base.metadata.create_all(engine)

    # When: lrclib settings are saved as one aggregate and rebuilt from persistence.
    with Session(engine) as session:
        save_runtime_settings(
            session,
            RuntimeSettings().model_copy(
                update={
                    'lrclib_enabled': False,
                    'lrclib_host': 'https://lrclib.internal',
                    'lrclib_user_agent': 'Music Ingest/0.1 (ops@example.com)',
                    'lrclib_timeout_seconds': 25.5,
                    'lrclib_max_attempts': 7,
                    'lrclib_request_delay_seconds': 1.25,
                    'lrclib_max_response_bytes': 512 * 1024,
                    'lrclib_match_confidence_threshold': 0.8,
                }
            ),
        )
        session.commit()
        persisted = build_runtime_settings(session)

    # Then: every lrclib key is written and read back, including the boolean as a lowercase scalar.
    assert persisted.lrclib_enabled is False
    assert persisted.lrclib_host == 'https://lrclib.internal'
    assert persisted.lrclib_user_agent == 'Music Ingest/0.1 (ops@example.com)'
    assert persisted.lrclib_timeout_seconds == 25.5
    assert persisted.lrclib_max_attempts == 7
    assert persisted.lrclib_request_delay_seconds == 1.25
    assert persisted.lrclib_max_response_bytes == 512 * 1024
    assert persisted.lrclib_match_confidence_threshold == 0.8
    with Session(engine) as session:
        assert get_setting_value(session, SettingKey.LRCLIB_ENABLED) == 'false'
        assert get_setting_value(session, SettingKey.LRCLIB_HOST) == 'https://lrclib.internal'
        assert get_setting_value(session, SettingKey.LRCLIB_MAX_RESPONSE_BYTES) == str(512 * 1024)
        assert get_setting_value(session, SettingKey.LRCLIB_MATCH_CONFIDENCE_THRESHOLD) == '0.8'


def test_setting_values_when_requested_keys_are_persisted_uses_one_query_and_omits_missing_keys(
    tmp_path: Path,
) -> None:
    # Given: two persisted settings and one requested key that is absent.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "scalar-settings.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add_all(
            (
                RuntimeSettingRecord(
                    key=SettingKey.MUSICBRAINZ_HOST.value,
                    value='https://musicbrainz.internal',
                    updated_at=datetime.now(UTC),
                ),
                RuntimeSettingRecord(
                    key=SettingKey.WORKER_POOLS.value,
                    value='{"final_publish":6}',
                    updated_at=datetime.now(UTC),
                ),
            )
        )
        session.commit()

    statements: list[str] = []

    def capture_select(_connection, _cursor, statement, _parameters, _context, _executemany) -> None:
        if statement.lstrip().upper().startswith('SELECT'):
            statements.append(statement)

    event.listen(engine, 'before_cursor_execute', capture_select)
    try:
        with Session(engine) as session:
            values = get_setting_values(
                session,
                (
                    SettingKey.MUSICBRAINZ_HOST,
                    SettingKey.WORKER_POOLS,
                    SettingKey.ACOUSTID_ENABLED,
                ),
            )
    finally:
        event.remove(engine, 'before_cursor_execute', capture_select)

    assert values == {
        SettingKey.MUSICBRAINZ_HOST: 'https://musicbrainz.internal',
        SettingKey.WORKER_POOLS: '{"final_publish":6}',
    }
    assert len(statements) == 1


def test_build_runtime_settings_when_values_are_missing_uses_defaults_without_loading_genres(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: an empty settings database and a genre loader that must not be touched.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "default-settings.db"}')
    Base.metadata.create_all(engine)

    def fail_if_genres_are_loaded(_session: Session) -> tuple[object, ...]:
        raise AssertionError('genre catalog should be opt-in')

    monkeypatch.setattr('music_ingest.services.settings.load_genre_catalog', fail_if_genres_are_loaded)
    with Session(engine) as session:
        settings = build_runtime_settings(session)
        missing = get_setting_value(session, SettingKey.MUSICBRAINZ_HOST)

    assert settings.musicbrainz_host == RuntimeSettings().musicbrainz_host
    assert settings.worker_pools == RuntimeSettings().worker_pools
    assert missing is None


def test_build_runtime_settings_when_lrclib_is_unconfigured_uses_the_live_adapter_defaults(tmp_path: Path) -> None:
    # Given: an empty settings database and the live lrclib adapter's own request surface.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lrclib-defaults.db"}')
    Base.metadata.create_all(engine)

    # When: runtime settings are rebuilt without any persisted scalar value.
    with Session(engine) as session:
        settings = build_runtime_settings(session)

    # Then: the defaults match the adapter's live host, user agent, size bound, and transport bounds.
    assert settings.lrclib_enabled is True
    assert lookup_url(LrclibLookupRequest('track', 'artist', 180)).startswith(f'{settings.lrclib_host}/api/')
    assert settings.lrclib_user_agent == DEFAULT_USER_AGENT
    assert settings.lrclib_max_response_bytes == MAX_RESPONSE_BODY_BYTES
    assert settings.lrclib_timeout_seconds == 15.0
    assert settings.lrclib_max_attempts == 3
    assert settings.lrclib_request_delay_seconds == 0.3


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


def test_runtime_configuration_when_rescan_interval_is_configured_uses_seconds() -> None:
    # Given: a valid database configuration and an explicit hourly-rescan interval.
    environment = {
        'MUSIC_INGEST_DATABASE_URL': 'postgresql+psycopg://music_ingest@database/music_ingest',
        'MUSIC_INGEST_RECONCILIATION_INTERVAL_SECONDS': '120',
    }

    # When: the service parses its startup configuration.
    runtime_config = RuntimeConfig.from_environment(environment)

    # Then: the configured interval is retained as a runtime value.
    assert runtime_config.reconciliation_interval_seconds == 120


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
    Base.metadata.create_all(engine)

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
    Base.metadata.create_all(engine)

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
    Base.metadata.create_all(engine)

    def capture_app(*_args: object, **kwargs: object):
        captured.update(kwargs)
        return server.FastAPI()

    monkeypatch.setattr(server, 'create_app', capture_app)

    # When: the runtime application is constructed.
    _ = server.create_runtime_app()

    # Then: the API receives the same transport used by the worker.
    assert captured['musicbrainz_transport'] is transport


def test_runtime_config_when_resolving_musicbrainz_host_reads_only_host_setting(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a persisted custom host and a runtime settings loader that must not be used by transport callbacks.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime-host.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            RuntimeSettingRecord(
                key='providers.musicbrainz.host', value='https://musicbrainz.internal', updated_at=datetime.now(UTC)
            )
        )
        session.commit()
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    captured: list[Callable[[], str]] = []

    def capture_transport(*, musicbrainz_host: Callable[[], str], **_kwargs: object) -> object:
        captured.append(musicbrainz_host)
        return object()

    monkeypatch.setattr(server, 'build_live_transport', capture_transport)
    monkeypatch.setattr(server, 'create_app', lambda *_args, **_kwargs: server.FastAPI())

    # When: the runtime composes its live provider transport.
    _ = server.create_runtime_app()

    # Then: the callback returns the persisted host without loading the genre catalog or other settings.
    assert captured[0]() == 'https://musicbrainz.internal'


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


def test_runtime_app_when_started_runs_the_reconciliation_scheduler(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a runtime with isolated long-lived worker and scheduler boundaries.
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10, 120)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "runtime.db"}')
    Base.metadata.create_all(engine)
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)
    monkeypatch.setattr(server, 'run_processing_worker', _record_worker_start)
    started: list[int] = []

    async def record_scheduler_start(_session_factory: Callable[[], Session], interval_seconds: int) -> None:
        started.append(interval_seconds)
        await anyio.sleep_forever()

    monkeypatch.setattr(server, 'run_reconciliation_scheduler', record_scheduler_start)

    # When: the ASGI lifespan starts.
    with TestClient(server.create_runtime_app()):
        pass

    # Then: the scheduler starts with the configured interval and is shut down with the app.
    assert started == [120]


def test_processing_runtime_when_started_supervises_all_configurable_worker_slots(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: the processing supervisor with its worker-slot boundary replaced by a cancellable observer.
    started: list[int] = []

    async def record_worker_slot(
        _session_factory: Callable[[], Session],
        _config: ProcessingConfig,
        worker_slot: int,
        local_slot: int,
        pool: str,
        kinds: frozenset[str],
        limiter: anyio.CapacityLimiter,
        _poll_seconds: float,
        _monitor: processing_runtime.ProcessingRuntimeMonitor | None,
        _settings: processing_runtime.PoolSettingsSnapshot,
    ) -> None:
        started.append(worker_slot)
        assert local_slot < processing_runtime.MAX_POOL_CONCURRENCY
        assert kinds == processing_runtime.WORKER_POOLS[pool]
        assert limiter.total_tokens == processing_runtime.MAX_POOL_CONCURRENCY
        await anyio.sleep_forever()

    monkeypatch.setattr(processing_runtime, '_run_processing_worker_slot', record_worker_slot)

    async def maintenance(_session_factory, _config, _poll_seconds) -> None:
        await anyio.sleep_forever()

    monkeypatch.setattr(processing_runtime, '_run_maintenance', maintenance)

    async def start_then_cancel() -> None:
        with anyio.move_on_after(0.1):
            await processing_runtime.run_processing_worker(
                lambda: Session(), ProcessingConfig(Path('/incoming'), Path('/staging'), Path('/media'))
            )

    # When: the supervisor starts.
    anyio.run(start_then_cancel)

    # Then: it creates every bounded slot so the live setting can activate up to the validated maximum.
    assert started == list(range(len(processing_runtime.WORKER_POOLS) * processing_runtime.MAX_POOL_CONCURRENCY))


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

    # When: the runtime-owned HTTP app receives an external change notification.
    with TestClient(server.create_runtime_app()) as client:
        response = client.post('/api/intake/notification', json={'paths': [str(source_path)]})

    # Then: the configured source root is reconciled into source-linked durable work.
    assert response.status_code == 202
    with Session(engine) as session:
        job = session.scalars(select(JobRecord)).one()
        assert job.kind == 'reconciliation_scan'


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
        assert bootstrap_root.parent.name.startswith('music-ingest-migration-')

    monkeypatch.setattr(server.command, 'upgrade', upgrade)
    monkeypatch.delenv('MUSIC_INGEST_INCOMING_ROOT', raising=False)

    # When: the runtime executes its migration boundary.
    server.run_migrations(runtime_config)

    # Then: an outdated database reaches head using an isolated migration bootstrap.
    assert captured == [(runtime_config.database_url, '7', 'head')]


def test_runtime_refuses_readiness_when_media_does_not_support_publication(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    runtime_config = RuntimeConfig('postgresql+psycopg://music_ingest@database/music_ingest', 10)
    engine = create_engine(f'sqlite:///{tmp_path / "readiness.db"}')
    Base.metadata.create_all(engine)
    monkeypatch.setattr(server, 'run_migrations', lambda _config: None)
    monkeypatch.setattr(server, 'create_engine', lambda *_args, **_kwargs: engine)
    monkeypatch.setattr(RuntimeConfig, 'from_environment', lambda _environment: runtime_config)

    def reject_storage(root: Path) -> None:
        raise ValueError(f'unsupported publication storage at {root}: atomic rename required')

    monkeypatch.setattr(server, 'validate_publication_storage', reject_storage)
    with (
        pytest.raises(RuntimeConfigurationError, match='unsupported publication storage'),
        TestClient(server.create_runtime_app()),
    ):
        pytest.fail('readiness must not be reached')
    engine.dispose()
