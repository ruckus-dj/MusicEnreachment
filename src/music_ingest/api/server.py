from __future__ import annotations

import os
from argparse import Namespace
from collections.abc import AsyncGenerator, Callable, Mapping
from contextlib import asynccontextmanager
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Self, final

import anyio
from alembic.config import Config
from fastapi import FastAPI
from sqlalchemy import create_engine
from sqlalchemy.engine import make_url
from sqlalchemy.exc import ArgumentError
from sqlalchemy.orm import Session, sessionmaker

from alembic import command
from music_ingest.api.app import create_app
from music_ingest.matching.providers import DatabaseRequestRateLimiter, ProviderName, build_live_transport
from music_ingest.models.repositories import ensure_provider_schedules
from music_ingest.processing import ProcessingConfig
from music_ingest.processing.runtime import ProcessingRuntimeMonitor, run_processing_worker
from music_ingest.settings import load_runtime_settings

_DATABASE_URL_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_DATABASE_URL'
_CONNECT_TIMEOUT_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_DATABASE_CONNECT_TIMEOUT_SECONDS'
_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_SOURCE_ROOTS_PARENT'
_STORAGE_BROWSE_ROOTS_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_STORAGE_BROWSE_ROOTS'
_DEFAULT_CONNECT_TIMEOUT_SECONDS = 10
_ALEMBIC_INI = Path(__file__).resolve().parents[3] / 'alembic.ini'


class RuntimeConfigurationError(RuntimeError):
    pass


@final
@dataclass(frozen=True, slots=True)
class RuntimeConfig:
    database_url: str
    connect_timeout_seconds: int

    @classmethod
    def from_environment(cls, environment: Mapping[str, str]) -> Self:
        database_url = environment.get(_DATABASE_URL_ENVIRONMENT_VARIABLE)
        if database_url is None:
            raise RuntimeConfigurationError(f'{_DATABASE_URL_ENVIRONMENT_VARIABLE} is required')
        try:
            parsed_url = make_url(database_url)
        except ArgumentError as error:
            raise RuntimeConfigurationError('MUSIC_INGEST_DATABASE_URL must be a PostgreSQL URL') from error
        if parsed_url.get_backend_name() != 'postgresql' or parsed_url.database is None:
            raise RuntimeConfigurationError('MUSIC_INGEST_DATABASE_URL must be a PostgreSQL database URL')
        raw_timeout = environment.get(_CONNECT_TIMEOUT_ENVIRONMENT_VARIABLE, str(_DEFAULT_CONNECT_TIMEOUT_SECONDS))
        try:
            connect_timeout_seconds = int(raw_timeout)
        except ValueError as error:
            raise RuntimeConfigurationError(
                f'{_CONNECT_TIMEOUT_ENVIRONMENT_VARIABLE} must be a positive integer'
            ) from error
        if connect_timeout_seconds < 1:
            raise RuntimeConfigurationError(f'{_CONNECT_TIMEOUT_ENVIRONMENT_VARIABLE} must be a positive integer')
        return cls(
            database_url=database_url,
            connect_timeout_seconds=connect_timeout_seconds,
        )


def run_migrations(runtime_config: RuntimeConfig) -> None:
    migration_config = Config(str(_ALEMBIC_INI))
    migration_config.set_main_option('sqlalchemy.url', runtime_config.database_url)
    migration_config.set_main_option(
        'music_ingest.connect_timeout_seconds', str(runtime_config.connect_timeout_seconds)
    )
    with TemporaryDirectory(prefix='music-ingest-migration-') as temporary_parent:
        parent = Path(temporary_parent)
        bootstrap_root = parent / 'bootstrap'
        bootstrap_root.mkdir()
        previous_parent = os.environ.get(_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE)
        os.environ[_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE] = str(parent)
        migration_config.cmd_opts = Namespace(x=[f'legacy_incoming_root={bootstrap_root}'])
        try:
            command.upgrade(migration_config, 'head')
        finally:
            if previous_parent is None:
                del os.environ[_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE]
            else:
                os.environ[_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE] = previous_parent


def create_runtime_app() -> FastAPI:
    runtime_config = RuntimeConfig.from_environment(os.environ)
    run_migrations(runtime_config)
    engine = create_engine(
        runtime_config.database_url,
        connect_args={'connect_timeout': runtime_config.connect_timeout_seconds},
        pool_pre_ping=True,
    )

    session_factory = sessionmaker(engine)
    processing_config = _processing_config(os.environ, session_factory)
    worker_monitor = ProcessingRuntimeMonitor()
    raw_source_roots_parent = os.environ.get(_SOURCE_ROOTS_PARENT_ENVIRONMENT_VARIABLE)
    source_roots_parent = None if raw_source_roots_parent is None else Path(raw_source_roots_parent)

    @asynccontextmanager
    async def lifespan(application: FastAPI) -> AsyncGenerator[None]:
        del application
        with session_factory() as session:
            ensure_provider_schedules(session, (provider.value for provider in ProviderName), datetime.now(UTC))
            session.commit()
        async with anyio.create_task_group() as task_group:
            _ = task_group.start_soon(run_processing_worker, session_factory, processing_config, 1.0, worker_monitor)
            try:
                yield
            finally:
                task_group.cancel_scope.cancel()
        engine.dispose()

    application = create_app(
        session_factory,
        lifespan=lifespan,
        source_roots_parent=source_roots_parent,
        media_root=processing_config.media_root,
        musicbrainz_provider=processing_config.musicbrainz_provider,
        musicbrainz_transport=processing_config.live_transport,
        genre_transport=processing_config.live_transport,
        e2e_seed_enabled=os.environ.get('MUSIC_INGEST_E2E_SEED_ENABLED') == 'true',
        storage_browse_roots=tuple(
            Path(item)
            for item in os.environ.get(_STORAGE_BROWSE_ROOTS_ENVIRONMENT_VARIABLE, '/data').split(':')
            if item
        ),
        worker_monitor=worker_monitor,
    )
    return application


def _processing_config(environment: Mapping[str, str], session_factory: Callable[[], Session]) -> ProcessingConfig:
    def musicbrainz_host() -> str:
        with session_factory() as session:
            return load_runtime_settings(session).musicbrainz_host

    live_transport = build_live_transport(
        limiter=DatabaseRequestRateLimiter(session_factory), musicbrainz_host=musicbrainz_host
    )
    return ProcessingConfig(
        incoming_root=Path('/data/incoming'),
        staging_root=Path(environment.get('MUSIC_INGEST_STAGING_ROOT', '/appdata/music-ingest/staging')),
        media_root=Path(environment.get('MUSIC_INGEST_MEDIA_ROOT', '/data/media')),
        live_transport=live_transport,
    )
