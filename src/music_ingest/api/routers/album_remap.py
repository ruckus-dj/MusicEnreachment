from __future__ import annotations

from hashlib import sha256
from uuid import UUID

from fastapi import APIRouter, HTTPException
from sqlalchemy.orm import Session

from music_ingest.adapters.external.musicbrainz import (
    MusicBrainzClient,
    SyncMusicBrainzTransport,
    ThreadedMusicBrainzTransport,
)
from music_ingest.api.dependencies import SessionFactory
from music_ingest.contracts import (
    AlbumReleaseSearchRequest,
    AlbumReleaseSearchResponse,
    AlbumRemapApplyRequest,
    AlbumRemapApplyResponse,
    AlbumRemapContextRequest,
    AlbumRemapContextResponse,
    AlbumRemapPreviewRequest,
    AlbumRemapPreviewResponse,
)
from music_ingest.services.album_remap import (
    AlbumRemapConflict,
    AlbumRemapRelease,
    apply_remap,
    load_context,
    preview_slots,
    release_summary,
)
from music_ingest.services.settings import build_runtime_settings


def create_router(
    session_factory: SessionFactory, *, musicbrainz_transport: SyncMusicBrainzTransport | None
) -> APIRouter:
    router = APIRouter()

    @router.post('/api/library/album-remaps/context', response_model=AlbumRemapContextResponse)
    def album_context(request: AlbumRemapContextRequest) -> AlbumRemapContextResponse:
        with session_factory() as session:
            return load_context(session, request.selector)

    @router.post('/api/library/album-remaps/releases/search', response_model=AlbumReleaseSearchResponse)
    async def search_releases(request: AlbumReleaseSearchRequest) -> AlbumReleaseSearchResponse:
        with session_factory() as session:
            client = _client(session, musicbrainz_transport)
            release_id = _uuid(request.query)
            if release_id is not None:
                selected = await _release(client, str(release_id))
                return AlbumReleaseSearchResponse(releases=(release_summary(selected.release),))
            result = await client.search_releases(request.query)
            if result.payload is None:
                raise HTTPException(status_code=503, detail='MusicBrainz release search is unavailable')
            return AlbumReleaseSearchResponse(
                releases=tuple(release_summary(release) for release in result.payload.releases)
            )

    @router.post('/api/library/album-remaps/preview', response_model=AlbumRemapPreviewResponse)
    async def preview(request: AlbumRemapPreviewRequest) -> AlbumRemapPreviewResponse:
        with session_factory() as session:
            context = load_context(session, request.selector)
            if context.album_snapshot_token != request.album_snapshot_token:
                raise HTTPException(status_code=409, detail='album snapshot is stale')
            selected = await _release(_client(session, musicbrainz_transport), str(request.release_mbid))
        try:
            return AlbumRemapPreviewResponse(
                release_snapshot_token=selected.snapshot_token,
                release=release_summary(selected.release),
                track_slots=preview_slots(context, selected.release),
            )
        except AlbumRemapConflict as error:
            raise HTTPException(status_code=503, detail=error.detail) from error

    @router.post('/api/library/album-remaps/apply', response_model=AlbumRemapApplyResponse)
    async def apply(request: AlbumRemapApplyRequest) -> AlbumRemapApplyResponse:
        with session_factory() as session:
            selected = await _release(_client(session, musicbrainz_transport), str(request.release_mbid))
        if selected.snapshot_token != request.release_snapshot_token:
            raise HTTPException(status_code=409, detail='release snapshot is stale')
        try:
            with session_factory() as session:
                response = apply_remap(session, request, selected)
                session.commit()
                return response
        except AlbumRemapConflict as error:
            raise HTTPException(status_code=409, detail=error.detail) from error

    return router


def _client(session: Session, transport: SyncMusicBrainzTransport | None) -> MusicBrainzClient:
    settings = build_runtime_settings(session)
    if not settings.musicbrainz_enabled:
        raise HTTPException(status_code=503, detail='MusicBrainz provider is disabled')
    if transport is None:
        raise HTTPException(status_code=503, detail='MusicBrainz provider is not configured')
    return MusicBrainzClient(
        ThreadedMusicBrainzTransport(transport), settings.musicbrainz_user_agent, host=settings.musicbrainz_host
    )


async def _release(client: MusicBrainzClient, release_mbid: str) -> AlbumRemapRelease:
    detail = await client.release_detail(release_mbid)
    release = detail.response.payload
    if release is None:
        raise HTTPException(status_code=503, detail='MusicBrainz release detail is unavailable')
    return AlbumRemapRelease(release, sha256(detail.response.body).hexdigest())


def _uuid(value: str) -> UUID | None:
    try:
        return UUID(value)
    except ValueError:
        return None
