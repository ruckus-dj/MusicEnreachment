from __future__ import annotations

import os
from collections.abc import AsyncGenerator, Mapping
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Self, final

import anyio
from alembic.config import Config
from fastapi import FastAPI
from sqlalchemy import create_engine
from sqlalchemy.engine import make_url
from sqlalchemy.exc import ArgumentError
from sqlalchemy.orm import sessionmaker

from alembic import command
from music_ingest.api.app import create_app
from music_ingest.matching.providers import build_live_transport
from music_ingest.processing import ProcessingConfig
from music_ingest.processing.runtime import run_processing_worker

_DATABASE_URL_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_DATABASE_URL'
_CONNECT_TIMEOUT_ENVIRONMENT_VARIABLE = 'MUSIC_INGEST_DATABASE_CONNECT_TIMEOUT_SECONDS'
_DEFAULT_CONNECT_TIMEOUT_SECONDS = 10
_ALEMBIC_INI = Path(__file__).resolve().parents[3] / 'alembic.ini'


class RuntimeConfigurationError(RuntimeError):
    pass


@final
@dataclass(frozen=True, slots=True)
class RuntimeConfig:
    database_url: str
    connect_timeout_seconds: int
    api_token: str | None = None

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
            api_token=environment.get('MUSIC_INGEST_API_TOKEN'),
        )


def run_migrations(runtime_config: RuntimeConfig) -> None:
    migration_config = Config(str(_ALEMBIC_INI))
    migration_config.set_main_option('sqlalchemy.url', runtime_config.database_url)
    migration_config.set_main_option(
        'music_ingest.connect_timeout_seconds', str(runtime_config.connect_timeout_seconds)
    )
    command.upgrade(migration_config, 'head')


def create_runtime_app() -> FastAPI:
    runtime_config = RuntimeConfig.from_environment(os.environ)
    run_migrations(runtime_config)
    engine = create_engine(
        runtime_config.database_url,
        connect_args={'connect_timeout': runtime_config.connect_timeout_seconds},
        pool_pre_ping=True,
    )

    session_factory = sessionmaker(engine)
    processing_config = _processing_config(os.environ)

    @asynccontextmanager
    async def lifespan(application: FastAPI) -> AsyncGenerator[None]:
        del application
        async with anyio.create_task_group() as task_group:
            _ = task_group.start_soon(run_processing_worker, session_factory, processing_config)
            try:
                yield
            finally:
                task_group.cancel_scope.cancel()
        engine.dispose()

    return create_app(
        session_factory,
        lifespan=lifespan,
        incoming_root=processing_config.incoming_root,
        media_root=processing_config.media_root,
        api_token=runtime_config.api_token,
        musicbrainz_provider=processing_config.musicbrainz_provider,
        genre_transport=processing_config.live_transport,
    )


def _processing_config(environment: Mapping[str, str]) -> ProcessingConfig:
    live_transport = build_live_transport()
    return ProcessingConfig(
        incoming_root=Path(environment.get('MUSIC_INGEST_INCOMING_ROOT', '/data/incoming')),
        staging_root=Path(environment.get('MUSIC_INGEST_STAGING_ROOT', '/appdata/music-ingest/staging')),
        media_root=Path(environment.get('MUSIC_INGEST_MEDIA_ROOT', '/data/media')),
        live_transport=live_transport,
    )
