from __future__ import annotations

import json
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Protocol

from fastapi import FastAPI, HTTPException, Request, Response, status
from fastapi.responses import HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy.orm import Session
from starlette.types import Lifespan

from music_ingest.api.lidarr_intake import LidarrIntakeError, dispatch_lidarr_event, parse_lidarr_event
from music_ingest.library.service import (
    append_metadata_revision,
    attach_source,
    library_record_detail,
    library_records,
    record_event,
)
from music_ingest.matching.scoring import DEFAULT_CONFIDENCE_THRESHOLD
from music_ingest.persistence.jobs import JobRepository
from music_ingest.persistence.library import LibraryRecord, SourceRecordView
from music_ingest.persistence.models import RuntimeSettingRecord
from music_ingest.persistence.repository import ReceiptReplayConflictError
from music_ingest.reconciliation import ScanResult, reconcile_incoming
from music_ingest.ui.page import REVIEW_PAGE


class SessionFactory(Protocol):
    def __call__(self) -> Session: ...


class MatchingSettings(BaseModel):
    model_config = ConfigDict(frozen=True)

    confidence_threshold: float = Field(ge=0.0, le=1.0)


class LibraryIdentityUpdate(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_recording_id: str = Field(
        pattern=r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$'
    )


class MetadataUpdate(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    tags: dict[str, str]


class ProviderRetryResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    queued: int


class ProviderRetryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    queued: bool


_RETRYABLE_PROVIDER_OUTCOMES = frozenset({'malformed', 'rate_limited', 'timeout', 'unavailable', 'disabled', 'failed'})


def _catalog_tags(record: LibraryRecord, source_id: str) -> dict[str, str]:
    for layer in ('final', 'original'):
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == source_id and item.layer == layer
            ),
            None,
        )
        if revision is not None:
            return json.loads(revision.tags_json)
    source = next(item for item in record.sources if item.id == source_id)
    return {tag.tag_name: tag.value for tag in source.tag_observations}


def _track_number(value: str | None) -> int | None:
    if value is None:
        return None
    try:
        return int(value.split('/', maxsplit=1)[0].strip())
    except ValueError:
        return None


def _catalog_sort_key(record: LibraryRecord) -> tuple[str, str, int, str, str, str]:
    source = record.sources[0]
    tags = _catalog_tags(record, source.id)
    track_number = _track_number(tags.get('TRACKNUMBER'))
    return (
        tags.get('ARTIST', 'Неизвестный исполнитель').strip().casefold(),
        tags.get('ALBUM', 'Без альбома').strip().casefold(),
        track_number if track_number is not None else 2**31 - 1,
        tags.get('TITLE', '').strip().casefold(),
        record.id,
        source.id,
    )


def _needs_provider_retry(source: SourceRecordView) -> bool:
    return not source.provider_attempts or any(
        attempt.outcome.casefold() in _RETRYABLE_PROVIDER_OUTCOMES for attempt in source.provider_attempts
    )


def create_app(
    session_factory: SessionFactory,
    lifespan: Lifespan[FastAPI] | None = None,
    incoming_root: Path = Path('/data/incoming'),
    media_root: Path | None = None,
    api_token: str | None = None,
) -> FastAPI:
    app = FastAPI(title='Music ingestion review', version='0.1.0', lifespan=lifespan)
    assets_root = Path(__file__).parents[1] / 'ui' / 'dist' / 'assets'
    if assets_root.is_dir():
        app.mount('/assets', StaticFiles(directory=assets_root), name='ui-assets')

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

    @app.get('/', response_class=HTMLResponse)
    @app.get('/review', response_class=HTMLResponse)
    def review_page() -> str:
        return REVIEW_PAGE

    @app.get('/library', response_class=HTMLResponse)
    @app.get('/library/{path:path}', response_class=HTMLResponse)
    def library_route(path: str = '') -> str:
        _ = path
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
                result = dispatch_lidarr_event(session, event, raw_payload, incoming_root)
                session.commit()
        except LidarrIntakeError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error
        except ReceiptReplayConflictError as error:
            raise HTTPException(status_code=409, detail=str(error)) from error
        if result.job_id is None:
            return Response(status_code=status.HTTP_204_NO_CONTENT)
        return JSONResponse(status_code=status.HTTP_202_ACCEPTED, content=result.model_dump())

    @app.post('/api/reconciliation/scan', response_model=ScanResult)
    def reconciliation_scan() -> ScanResult:
        with session_factory() as session:
            result = reconcile_incoming(session, incoming_root)
            session.commit()
            return result

    @app.post('/api/library/providers/retry', response_model=ProviderRetryResult)
    def retry_failed_providers() -> ProviderRetryResult:
        now = datetime.now(UTC)
        queued = 0
        with session_factory() as session:
            jobs = JobRepository(session)
            for record in library_records(session):
                for source in record.sources:
                    if source.disappeared_at is not None or not _needs_provider_retry(source):
                        continue
                    if jobs.requeue_source(source.id, now):
                        record_event(
                            session,
                            record.id,
                            'provider_retry_queued',
                            'queued',
                            'provider retry requested from review UI',
                            now,
                            source.id,
                        )
                        queued += 1
            session.commit()
        return ProviderRetryResult(queued=queued)

    @app.get('/api/library/records')
    def library_catalog() -> JSONResponse:
        with session_factory() as session:
            records = sorted(library_records(session), key=_catalog_sort_key)
            return JSONResponse(
                content={
                    'items': [
                        {
                            'record_id': record.id,
                            'musicbrainz_recording_id': record.musicbrainz_recording_id,
                            'musicbrainz_release_id': record.musicbrainz_release_id,
                            'musicbrainz_artist_id': record.musicbrainz_artist_id,
                            'source_state': record.source_state,
                            'processing_state': record.processing_state,
                            'match_state': record.match_state,
                            'publication_state': record.publication_state,
                            'metadata_state': record.metadata_state,
                            'metadata_revisions': [
                                {
                                    'source_id': revision.source_id,
                                    'layer': revision.layer,
                                    'revision': revision.revision,
                                    'tags': json.loads(revision.tags_json),
                                }
                                for revision in record.metadata_revisions
                            ],
                            'sources': [
                                {
                                    'source_id': source.id,
                                    'path': source.source_path,
                                    'format': source.source_path.rsplit('.', maxsplit=1)[-1],
                                    'sha256': source.sha256,
                                    'state': source.intake_state,
                                    'tag_observations': [
                                        {'name': tag.tag_name, 'value': tag.value, 'format': tag.format_name}
                                        for tag in source.tag_observations
                                    ],
                                    'disappeared_at': source.disappeared_at.isoformat()
                                    if source.disappeared_at is not None
                                    else None,
                                }
                                for source in record.sources
                            ],
                            'publications': [
                                {
                                    'publication_id': publication.id,
                                    'source_id': publication.source_id,
                                    'path': publication.path,
                                    'format': publication.format_name,
                                    'sha256': publication.content_sha256,
                                    'state': publication.state,
                                    'created_at': publication.created_at.isoformat(),
                                }
                                for publication in record.publications
                            ],
                        }
                        for record in records
                    ]
                }
            )

    @app.get('/api/library/records/{record_id}')
    def library_catalog_detail(record_id: str) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                return JSONResponse(
                    content={
                        'record_id': record.id,
                        'musicbrainz_recording_id': record.musicbrainz_recording_id,
                        'musicbrainz_release_id': record.musicbrainz_release_id,
                        'musicbrainz_artist_id': record.musicbrainz_artist_id,
                        'states': {
                            'source': record.source_state,
                            'processing': record.processing_state,
                            'match': record.match_state,
                            'publication': record.publication_state,
                            'metadata': record.metadata_state,
                        },
                        'sources': [
                            {
                                'source_id': source.id,
                                'path': source.source_path,
                                'sha256': source.sha256,
                                'size_bytes': source.size_bytes,
                                'origin': source.origin,
                                'state': source.intake_state,
                                'tag_observations': [
                                    {'name': tag.tag_name, 'value': tag.value, 'format': tag.format_name}
                                    for tag in source.tag_observations
                                ],
                                'fingerprints': [
                                    {
                                        'state': fingerprint.state,
                                        'fingerprint': fingerprint.fingerprint,
                                        'duration_seconds': fingerprint.duration_seconds,
                                        'tool_version': fingerprint.tool_version,
                                    }
                                    for fingerprint in source.fingerprints
                                ],
                                'provider_attempts': [
                                    {
                                        'provider': attempt.provider_name,
                                        'outcome': attempt.outcome,
                                        'snapshot_sha256': attempt.snapshot_sha256,
                                    }
                                    for attempt in source.provider_attempts
                                ],
                                'disappeared_at': source.disappeared_at.isoformat()
                                if source.disappeared_at is not None
                                else None,
                            }
                            for source in record.sources
                        ],
                        'publications': [
                            {
                                'publication_id': publication.id,
                                'source_id': publication.source_id,
                                'path': publication.path,
                                'format': publication.format_name,
                                'sha256': publication.content_sha256,
                                'metadata_revision_id': publication.metadata_revision_id,
                                'state': publication.state,
                                'created_at': publication.created_at.isoformat(),
                            }
                            for publication in record.publications
                        ],
                        'metadata_revisions': [
                            {
                                'id': revision.id,
                                'source_id': revision.source_id,
                                'layer': revision.layer,
                                'revision': revision.revision,
                                'tags': json.loads(revision.tags_json),
                                'actor': revision.actor,
                                'created_at': revision.created_at.isoformat(),
                            }
                            for revision in record.metadata_revisions
                        ],
                        'events': [
                            {
                                'id': event.id,
                                'source_id': event.source_id,
                                'kind': event.kind,
                                'state': event.state,
                                'reason': event.reason,
                                'details': json.loads(event.details_json),
                                'created_at': event.created_at.isoformat(),
                            }
                            for event in record.events
                        ],
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error

    @app.post('/api/library/records/{record_id}/sources/{source_id}', status_code=status.HTTP_204_NO_CONTENT)
    def attach_library_source(record_id: str, source_id: str) -> Response:
        try:
            with session_factory() as session:
                attach_source(session, source_id, record_id)
                session.commit()
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error
        return Response(status_code=status.HTTP_204_NO_CONTENT)

    @app.post('/api/library/records/{record_id}/sources/{source_id}/provider-retry')
    def retry_provider_for_source(record_id: str, source_id: str) -> ProviderRetryResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                if source.disappeared_at is not None:
                    return ProviderRetryResponse(source_id=source.id, queued=False)
                now = datetime.now(UTC)
                queued = JobRepository(session).requeue_source(source.id, now)
                if queued:
                    record_event(
                        session,
                        record.id,
                        'provider_retry_queued',
                        'queued',
                        'provider retry requested from review UI',
                        now,
                        source.id,
                    )
                session.commit()
                return ProviderRetryResponse(source_id=source.id, queued=queued)
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.put('/api/library/records/{record_id}/identity')
    def update_library_identity(record_id: str, request: LibraryIdentityUpdate) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                record.musicbrainz_recording_id = request.musicbrainz_recording_id.lower()
                record.match_state = 'matched'
                record.updated_at = datetime.now(UTC)
                record_event(
                    session,
                    record.id,
                    'identity_attached',
                    record.processing_state,
                    None,
                    record.updated_at,
                )
                session.commit()
                return JSONResponse(
                    content={'record_id': record.id, 'musicbrainz_recording_id': record.musicbrainz_recording_id}
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error

    @app.put('/api/library/records/{record_id}/metadata')
    def update_library_metadata(record_id: str, request: MetadataUpdate) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == request.source_id), None)
                if source is None:
                    raise HTTPException(status_code=404, detail='source not found')
                now = datetime.now(UTC)
                created = append_metadata_revision(
                    session,
                    record.id,
                    request.source_id,
                    'final',
                    request.tags,
                    'manual',
                    now,
                )
                queued = JobRepository(session).enqueue(source.id, 'final_publish', now, created.id)
                record_event(
                    session,
                    record.id,
                    'final_publish_queued',
                    'publishing',
                    'manual final metadata revision queued for publication',
                    now,
                    request.source_id,
                )
                session.commit()
                return JSONResponse(
                    content={'revision': created.revision, 'tags': request.tags, 'queued': queued is not None}
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error

    @app.get('/api/settings/matching', response_model=MatchingSettings)
    def matching_settings() -> MatchingSettings:
        with session_factory() as session:
            setting = session.get(RuntimeSettingRecord, 'matching.confidence_threshold')
            threshold = DEFAULT_CONFIDENCE_THRESHOLD if setting is None else float(setting.value)
            return MatchingSettings(confidence_threshold=threshold)

    @app.put('/api/settings/matching', response_model=MatchingSettings)
    def update_matching_settings(request: MatchingSettings) -> MatchingSettings:
        with session_factory() as session:
            setting = session.get(RuntimeSettingRecord, 'matching.confidence_threshold')
            if setting is None:
                session.add(
                    RuntimeSettingRecord(
                        key='matching.confidence_threshold',
                        value=str(request.confidence_threshold),
                        updated_at=datetime.now(UTC),
                    )
                )
            else:
                setting.value = str(request.confidence_threshold)
                setting.updated_at = datetime.now(UTC)
            session.commit()
            return request

    return app
