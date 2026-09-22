from __future__ import annotations

from collections.abc import Callable
from pathlib import Path

from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles
from starlette.types import Lifespan

from music_ingest.adapters.external.musicbrainz import SyncMusicBrainzTransport
from music_ingest.api.dependencies import SessionFactory
from music_ingest.api.routers import (
    catalog,
    e2e,
    genres,
    intake,
    matching,
    metadata,
    pages,
    reconciliation,
    recovery,
    settings,
    workers,
)
from music_ingest.services.matching.providers import MusicBrainzProvider
from music_ingest.services.settings import RuntimeSettings
from music_ingest.workers.runtime import ProcessingRuntimeMonitor


def create_app(
    session_factory: SessionFactory,
    lifespan: Lifespan[FastAPI] | None = None,
    source_roots_parent: Path | None = None,
    media_root: Path | None = None,
    e2e_seed_enabled: bool = False,
    musicbrainz_provider: MusicBrainzProvider | None = None,
    musicbrainz_transport: SyncMusicBrainzTransport | None = None,
    genre_transport: SyncMusicBrainzTransport | None = None,
    storage_browse_roots: tuple[Path, ...] | None = None,
    worker_monitor: ProcessingRuntimeMonitor | None = None,
    on_runtime_settings_updated: Callable[[RuntimeSettings], None] | None = None,
) -> FastAPI:
    """Compose application-local routers without changing request transaction ownership."""
    app = FastAPI(title='Music ingestion review', version='0.1.0', lifespan=lifespan)
    app.state.e2e_seed_enabled = e2e_seed_enabled
    assets_root = Path(__file__).parents[1] / 'static' / 'dist' / 'assets'
    if assets_root.is_dir():
        app.mount('/assets', StaticFiles(directory=assets_root), name='ui-assets')

    app.include_router(pages.create_router())
    app.include_router(e2e.create_router(session_factory))
    app.include_router(intake.create_router(session_factory))
    app.include_router(reconciliation.create_router(session_factory))
    app.include_router(workers.create_router(session_factory, worker_monitor=worker_monitor))
    app.include_router(recovery.create_router(session_factory, media_root=media_root))
    app.include_router(catalog.create_router(session_factory, media_root=media_root))
    app.include_router(
        matching.create_router(
            session_factory,
            musicbrainz_provider=musicbrainz_provider,
            musicbrainz_transport=musicbrainz_transport,
        )
    )
    app.include_router(metadata.create_router(session_factory))
    app.include_router(
        settings.create_router(
            session_factory,
            source_roots_parent=source_roots_parent,
            media_root=media_root,
            storage_browse_roots=storage_browse_roots,
            on_runtime_settings_updated=on_runtime_settings_updated,
        )
    )
    app.include_router(genres.create_router(session_factory, genre_transport=genre_transport))
    return app
