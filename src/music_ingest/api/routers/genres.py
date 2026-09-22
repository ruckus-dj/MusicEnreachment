from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException

from music_ingest.adapters.external.musicbrainz import SyncMusicBrainzTransport
from music_ingest.api.dependencies import SessionFactory
from music_ingest.contracts import GenreCatalogItemResponse, GenreCatalogResponse
from music_ingest.models import GenreCatalogRecord
from music_ingest.services.matching.genre_catalog import load_genre_catalog, replace_genre_catalog
from music_ingest.services.matching.genres import GenreCatalogSyncError, sync_genres
from music_ingest.services.settings import build_runtime_settings


def _genre_catalog_response(entries: tuple[GenreCatalogRecord, ...]) -> GenreCatalogResponse:
    return GenreCatalogResponse(
        items=tuple(
            GenreCatalogItemResponse(
                musicbrainz_id=entry.musicbrainz_id,
                source_name=entry.source_name,
                display_name=entry.display_name,
            )
            for entry in entries
        ),
        last_synced_at=entries[0].synced_at if entries else None,
    )


def create_router(
    session_factory: SessionFactory,
    *,
    genre_transport: SyncMusicBrainzTransport | None = None,
) -> APIRouter:
    router = APIRouter()

    @router.get('/api/genres', response_model=GenreCatalogResponse)
    def genre_catalog() -> GenreCatalogResponse:
        with session_factory() as session:
            entries = load_genre_catalog(session)
            if not entries and genre_transport is not None:
                settings = build_runtime_settings(session)
                try:
                    synced_entries = sync_genres(
                        genre_transport, user_agent=settings.musicbrainz_user_agent, host=settings.musicbrainz_host
                    )
                except GenreCatalogSyncError:
                    return _genre_catalog_response(entries)
                replace_genre_catalog(session, synced_entries, datetime.now(UTC))
                session.commit()
                entries = load_genre_catalog(session)
            return _genre_catalog_response(entries)

    @router.post('/api/genres/sync', response_model=GenreCatalogResponse)
    def sync_genre_catalog() -> GenreCatalogResponse:
        if genre_transport is None:
            raise HTTPException(status_code=503, detail='MusicBrainz genre sync is unavailable')
        with session_factory() as session:
            settings = build_runtime_settings(session)
            try:
                entries = sync_genres(
                    genre_transport, user_agent=settings.musicbrainz_user_agent, host=settings.musicbrainz_host
                )
            except GenreCatalogSyncError as error:
                raise HTTPException(status_code=502, detail=str(error)) from error
            synced_at = datetime.now(UTC)
            replace_genre_catalog(session, entries, synced_at)
            session.commit()
            return _genre_catalog_response(load_genre_catalog(session))

    return router
