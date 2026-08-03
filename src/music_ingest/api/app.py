from __future__ import annotations

from collections.abc import Awaitable, Callable
from pathlib import Path
from typing import Protocol

from fastapi import FastAPI, HTTPException, Request, Response, status
from fastapi.responses import HTMLResponse, JSONResponse
from pydantic import BaseModel, ConfigDict, Field, TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload
from starlette.types import Lifespan

from music_ingest.api.lidarr_intake import LidarrIntakeError, dispatch_lidarr_event, parse_lidarr_event
from music_ingest.persistence.models import SourceRecord
from music_ingest.persistence.repository import ReceiptReplayConflictError
from music_ingest.review.queue import QueueState, ReviewInputError, get_or_create, perform_action
from music_ingest.review.releases import ReleaseReviewConflict, ReleaseReviewInputError
from music_ingest.review.releases import detail as release_detail
from music_ingest.review.releases import edit as edit_release
from music_ingest.review.releases import queue as release_queue
from music_ingest.review.releases import republish as republish_release
from music_ingest.review.releases import rollback as rollback_release
from music_ingest.ui.page import REVIEW_PAGE


class SessionFactory(Protocol):
    def __call__(self) -> Session: ...


class ReviewAction(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_id: str | None = None
    artist: str | None = Field(default=None, max_length=512)
    release_title: str | None = Field(default=None, max_length=512)


class ReleaseTagEdit(BaseModel):
    model_config = ConfigDict(frozen=True)

    revision: int = Field(ge=1)
    tags: dict[str, str] = Field(min_length=1)


class ReleaseRevisionAction(BaseModel):
    model_config = ConfigDict(frozen=True)

    revision: int = Field(ge=1)


_FIELDS_ADAPTER = TypeAdapter(dict[str, str])


def create_app(
    session_factory: SessionFactory,
    lifespan: Lifespan[FastAPI] | None = None,
    incoming_root: Path = Path('/data/incoming'),
    provenance_root: Path | None = None,
    media_root: Path | None = None,
    api_token: str | None = None,
) -> FastAPI:
    app = FastAPI(title='Music ingestion review', version='0.1.0', lifespan=lifespan)
    source_provenance_root = provenance_root or incoming_root.parent / 'provenance'

    @app.middleware('http')
    async def authenticate_api(request: Request, call_next: Callable[[Request], Awaitable[Response]]) -> Response:
        if api_token is None or not request.url.path.startswith('/api/'):
            return await call_next(request)
        supplied = request.headers.get('X-API-Key')
        authorization = request.headers.get('Authorization')
        if supplied != api_token and authorization != f'Bearer {api_token}':
            return JSONResponse(status_code=status.HTTP_401_UNAUTHORIZED, content={'detail': 'authentication required'})
        return await call_next(request)

    @app.get('/healthz')
    def healthz() -> dict[str, str]:
        return {'status': 'ok', 'service': 'music-ingest'}

    @app.get('/review', response_class=HTMLResponse)
    def review_page() -> str:
        return REVIEW_PAGE

    @app.get('/favicon.ico')
    def favicon() -> Response:
        return Response(status_code=204)

    @app.post('/api/intake/lidarr', status_code=status.HTTP_202_ACCEPTED)
    async def lidarr_intake(request: Request) -> Response:
        raw_payload = await request.body()
        try:
            event = parse_lidarr_event(raw_payload)
            with session_factory() as session:
                result = dispatch_lidarr_event(session, event, raw_payload, incoming_root, source_provenance_root)
                session.commit()
        except LidarrIntakeError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error
        except ReceiptReplayConflictError as error:
            raise HTTPException(status_code=409, detail=str(error)) from error
        if result.job_id is None:
            return Response(status_code=status.HTTP_204_NO_CONTENT)
        return JSONResponse(status_code=status.HTTP_202_ACCEPTED, content=result.model_dump())

    @app.get('/api/review/queue')
    def review_queue(state: QueueState | None = None) -> dict[str, list[dict[str, str]]]:
        with session_factory() as session:
            sources = session.scalars(select(SourceRecord).order_by(SourceRecord.id)).all()
            items = []
            for source in sources:
                record = get_or_create(session, source.id)
                if state is None or record.state == state.value:
                    items.append({'source_id': source.id, 'state': record.state})
            session.commit()
            return {'items': items}

    @app.get('/api/review/items/{source_id}')
    def review_detail(source_id: str) -> JSONResponse:
        with session_factory() as session:
            source = session.scalar(
                select(SourceRecord)
                .where(SourceRecord.id == source_id)
                .options(
                    selectinload(SourceRecord.tag_observations),
                    selectinload(SourceRecord.provider_attempts),
                    selectinload(SourceRecord.candidates),
                    selectinload(SourceRecord.fingerprints),
                    selectinload(SourceRecord.review_release),
                    selectinload(SourceRecord.review_audits),
                    selectinload(SourceRecord.publish_snapshots),
                )
            )
            if source is None:
                raise HTTPException(status_code=404, detail='review item not found')
            record = get_or_create(session, source_id)
            original = _FIELDS_ADAPTER.validate_json(record.original_json)
            proposed = _FIELDS_ADAPTER.validate_json(record.proposed_json)
            detail = {
                'source_id': source_id,
                'state': record.state,
                'original': original,
                'proposed': proposed,
                'diff': [
                    {'field': key, 'original': original.get(key), 'proposed': proposed.get(key)} for key in proposed
                ],
                'provider': [
                    {'name': item.provider_name, 'state': item.outcome, 'snapshot': item.snapshot}
                    for item in source.provider_attempts
                ],
                'fingerprints': [
                    {'state': item.state, 'fingerprint': item.fingerprint, 'duration': item.duration_seconds}
                    for item in source.fingerprints
                ],
                'candidates': [{'key': item.candidate_key, 'evidence': item.evidence} for item in source.candidates],
                'audit': [
                    {'action': item.action, 'actor': item.actor, 'before': item.before_json, 'after': item.after_json}
                    for item in source.review_audits
                ],
                'ids': {'artist': record.artist_id, 'release': record.release_id, 'track': record.track_id},
                'musicbrainz_id': record.musicbrainz_id,
                'previous_publish_snapshot': _snapshot(source),
            }
            session.commit()
            return JSONResponse(content=detail)

    @app.post('/api/review/items/{source_id}/actions/{action}')
    def review_action(source_id: str, action: str, request: ReviewAction) -> dict[str, object]:
        fields = request.model_dump(exclude_none=True)
        try:
            with session_factory() as session:
                record = perform_action(session, source_id, action, fields)
                session.commit()
                return {
                    'source_id': source_id,
                    'state': record.state,
                    'ids': {'artist': record.artist_id, 'release': record.release_id, 'track': record.track_id},
                    'musicbrainz_id': record.musicbrainz_id,
                }
        except LookupError as error:
            raise HTTPException(status_code=404, detail='review item not found') from error
        except ReviewInputError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.get('/api/release-review/queue')
    def release_review_queue() -> dict[str, list[dict[str, str]]]:
        with session_factory() as session:
            return {'items': release_queue(session)}

    @app.get('/api/release-review/releases/{release_id}')
    def release_review_detail(release_id: str) -> JSONResponse:
        try:
            with session_factory() as session:
                return JSONResponse(content=release_detail(session, release_id))
        except LookupError as error:
            raise HTTPException(status_code=404, detail='release review item not found') from error

    @app.patch('/api/release-review/releases/{release_id}/tags')
    def release_review_edit(release_id: str, request: ReleaseTagEdit) -> dict[str, str | int]:
        try:
            with session_factory() as session:
                revision = edit_release(session, release_id, request.revision, request.tags, media_root)
                session.commit()
                return {'release_id': release_id, 'revision': revision, 'publication_state': 'published'}
        except LookupError as error:
            raise HTTPException(status_code=404, detail='release review item not found') from error
        except ReleaseReviewConflict as error:
            raise HTTPException(status_code=409, detail=str(error)) from error
        except ReleaseReviewInputError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.post('/api/release-review/releases/{release_id}/republish')
    def release_review_republish(release_id: str, request: ReleaseRevisionAction) -> dict[str, str | int]:
        try:
            with session_factory() as session:
                republish_release(session, release_id, request.revision, media_root)
                session.commit()
                return {'release_id': release_id, 'revision': request.revision, 'publication_state': 'published'}
        except LookupError as error:
            raise HTTPException(status_code=404, detail='release review item not found') from error
        except ReleaseReviewConflict as error:
            raise HTTPException(status_code=409, detail=str(error)) from error

    @app.post('/api/release-review/releases/{release_id}/rollback')
    def release_review_rollback(release_id: str, request: ReleaseRevisionAction) -> dict[str, str | int]:
        try:
            with session_factory() as session:
                revision = rollback_release(session, release_id, request.revision, media_root)
                session.commit()
                return {'release_id': release_id, 'revision': revision, 'publication_state': 'published'}
        except LookupError as error:
            raise HTTPException(status_code=404, detail='release review item not found') from error
        except ReleaseReviewInputError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    return app


def _snapshot(source: SourceRecord) -> dict[str, str] | None:
    if not source.publish_snapshots:
        return None
    item = sorted(source.publish_snapshots, key=lambda value: value.id)[-1]
    return {'state': item.state, 'release': item.release_json}
