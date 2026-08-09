from __future__ import annotations

import json
import shutil
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Literal, Protocol

from fastapi import FastAPI, HTTPException, Request, Response, status
from fastapi.responses import HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import select
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
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.matching.providers import Ambiguous, FixtureCase, MusicBrainzMatch, MusicBrainzProvider
from music_ingest.matching.scoring import DEFAULT_CONFIDENCE_THRESHOLD
from music_ingest.persistence.jobs import JobRepository
from music_ingest.persistence.library import LibraryRecord, SourceRecordView
from music_ingest.persistence.models import JobRecord, ReviewDecisionRecord, RuntimeSettingRecord
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


class ProviderRetryRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    retry_all: bool = False
    provider: Literal['acoustid', 'musicbrainz'] | None = None


class ProviderRetryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    source_id: str
    queued: bool


class CandidateSelection(BaseModel):
    model_config = ConfigDict(frozen=True)

    candidate_key: str = Field(min_length=1, max_length=255)
    provider: Literal['acoustid', 'musicbrainz'] = 'musicbrainz'


class MusicBrainzOverride(BaseModel):
    model_config = ConfigDict(frozen=True)

    release_mbid: str = Field(min_length=1, max_length=36)


class CandidateEvidencePayload(BaseModel):
    model_config = ConfigDict(frozen=True)

    provider: str = 'musicbrainz'
    artist: str = ''
    release: str = ''
    title: str = ''
    album: str = ''
    score: float | None = None
    tags: dict[str, str] = Field(default_factory=dict)


_DEFAULT_PROVIDER_RETRY_REQUEST = ProviderRetryRequest()


class RecoveryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    queued: int
    skipped: int
    conflicts: int


class SourceRecoveryResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    record_id: str
    source_id: str
    queued: bool
    kind: str | None


class DestinationConflictCleanupResponse(BaseModel):
    record_id: str
    source_id: str
    path: str
    removed: bool
    queued: bool


_RETRYABLE_PROVIDER_OUTCOMES = frozenset(
    {'malformed', 'rate_limited', 'ratelimited', 'timeout', 'unavailable', 'disabled', 'failed'}
)


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


def _candidate_is_displayable(evidence: CandidateEvidencePayload) -> bool:
    if evidence.provider != 'musicbrainz':
        return True
    return bool(
        evidence.artist.strip() and evidence.release.strip() and evidence.tags.get('MUSICBRAINZ_ALBUMID', '').strip()
    )


def create_app(
    session_factory: SessionFactory,
    lifespan: Lifespan[FastAPI] | None = None,
    incoming_root: Path = Path('/data/incoming'),
    media_root: Path | None = None,
    api_token: str | None = None,
    musicbrainz_provider: MusicBrainzProvider | None = None,
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
    @app.get('/favicon.svg')
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

    def destination_conflict(
        session: Session, record: LibraryRecord, source: SourceRecordView
    ) -> dict[str, str] | None:
        if media_root is None:
            return None
        publications = [item for item in record.publications if item.state == 'current']
        if any(Path(item.path).resolve().exists() for item in publications):
            return None
        jobs = list(
            session.scalars(
                select(JobRecord)
                .where(JobRecord.source_id == source.id, JobRecord.kind.not_in(['provider_analysis', 'final_publish']))
                .order_by(JobRecord.created_at.desc())
            ).all()
        )
        if not jobs:
            return None
        candidate = (media_root.resolve() / jobs[0].id).resolve()
        if candidate == media_root.resolve() or media_root.resolve() not in candidate.parents or not candidate.exists():
            return None
        ownership = 'managed' if jobs[0].id == candidate.name and jobs[0].kind == 'filesystem_scan' else 'unmanaged'
        return {'path': str(candidate), 'ownership': ownership, 'reason': 'media destination already exists'}

    def queue_source_recovery(
        session: Session, record: LibraryRecord, source: SourceRecordView, now: datetime
    ) -> str | None:
        source_path = Path(source.source_path)
        if source.disappeared_at is not None or not source_path.is_file():
            return None
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        final_revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == source.id and item.layer == 'final'
            ),
            None,
        )
        analyzed_revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == source.id and item.layer == 'analyzed'
            ),
            None,
        )
        if record.processing_state == 'complete':
            if analyzed_revision is None or not any(
                not name.startswith('MUSICBRAINZ_') for name in json.loads(analyzed_revision.tags_json)
            ):
                return None
            kind = 'provider_analysis'
        elif current_publication is None:
            kind = 'final_publish' if final_revision is not None else 'filesystem_scan'
        elif record.processing_state == 'publishing' or record.publication_state in {'stale', 'failed'}:
            kind = 'final_publish'
        elif record.processing_state != 'complete':
            kind = 'provider_analysis'
        else:
            return None
        job = JobRepository(session).enqueue(
            source.id,
            kind,
            now,
            final_revision.id if kind == 'final_publish' and final_revision is not None else None,
        )
        if job is None:
            return None
        record_event(
            session,
            record.id,
            'manual_recovery_queued',
            'publishing' if kind == 'final_publish' else 'queued',
            f'manual recovery queued {kind}',
            now,
            source.id,
        )
        return kind

    def queue_record_recovery(session: Session, record: LibraryRecord, now: datetime) -> tuple[int, int]:
        queued = 0
        conflicts = 0
        for source in record.sources:
            if source.disappeared_at is not None:
                continue
            if destination_conflict(session, record, source) is not None:
                conflicts += 1
                continue
            if queue_source_recovery(session, record, source, now) is not None:
                queued += 1
        return queued, conflicts

    @app.post('/api/library/recovery', response_model=RecoveryResponse)
    def recover_library() -> RecoveryResponse:
        now = datetime.now(UTC)
        queued = skipped = conflicts = 0
        with session_factory() as session:
            for record in library_records(session):
                record_queued, record_conflicts = queue_record_recovery(session, record, now)
                queued += record_queued
                conflicts += record_conflicts
                if record_queued == 0 and record_conflicts == 0:
                    skipped += 1
            session.commit()
        return RecoveryResponse(queued=queued, skipped=skipped, conflicts=conflicts)

    @app.post('/api/library/records/{record_id}/sources/{source_id}/reprocess', response_model=SourceRecoveryResponse)
    def reprocess_source(record_id: str, source_id: str) -> SourceRecoveryResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                conflict = destination_conflict(session, record, source)
                if conflict is not None:
                    raise HTTPException(status_code=409, detail='destination conflict must be replaced first')
                now = datetime.now(UTC)
                kind = queue_source_recovery(session, record, source, now)
                session.commit()
                return SourceRecoveryResponse(
                    record_id=record.id, source_id=source.id, queued=kind is not None, kind=kind
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.post('/api/library/providers/retry', response_model=ProviderRetryResult)
    def retry_failed_providers(request: ProviderRetryRequest = _DEFAULT_PROVIDER_RETRY_REQUEST) -> ProviderRetryResult:
        now = datetime.now(UTC)
        queued = 0
        with session_factory() as session:
            jobs = JobRepository(session)
            for record in library_records(session):
                for source in record.sources:
                    if source.disappeared_at is not None or (
                        not request.retry_all and not _needs_provider_retry(source)
                    ):
                        continue
                    provider_queued = (
                        jobs.requeue_provider(source.id, request.provider, now)
                        if request.provider is not None
                        else jobs.requeue_source(source.id, now)
                    )
                    if provider_queued:
                        record_event(
                            session,
                            record.id,
                            'provider_retry_queued',
                            'queued',
                            f'{request.provider or "all providers"} retry requested from review UI',
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
                                'candidates': [
                                    {
                                        'candidate_key': candidate.candidate_key,
                                        'evidence': CandidateEvidencePayload.model_validate_json(
                                            candidate.evidence
                                        ).model_dump(),
                                    }
                                    for candidate in source.candidates
                                    if _candidate_is_displayable(
                                        CandidateEvidencePayload.model_validate_json(candidate.evidence)
                                    )
                                ],
                                'review_decisions': [
                                    {
                                        'state': decision.state,
                                        'rationale': decision.rationale,
                                    }
                                    for decision in source.review_decisions
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
                        'destination_conflict': next(
                            (
                                conflict
                                for source in record.sources
                                if (conflict := destination_conflict(session, record, source)) is not None
                            ),
                            None,
                        ),
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error

    @app.post(
        '/api/library/records/{record_id}/sources/{source_id}/destination-conflict/cleanup',
        response_model=DestinationConflictCleanupResponse,
    )
    def cleanup_destination_conflict(record_id: str, source_id: str) -> DestinationConflictCleanupResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                conflict = destination_conflict(session, record, source)
                if conflict is None:
                    raise HTTPException(status_code=404, detail='destination conflict not found')
                if conflict['ownership'] == 'managed':
                    raise HTTPException(status_code=409, detail='managed destination must be replaced by the worker')
                path = Path(conflict['path'])
                if path.is_dir():
                    shutil.rmtree(path)
                else:
                    path.unlink()
                job = session.scalar(
                    select(JobRecord)
                    .where(
                        JobRecord.source_id == source.id, JobRecord.kind.not_in(['provider_analysis', 'final_publish'])
                    )
                    .order_by(JobRecord.created_at.desc())
                )
                queued = job is not None
                if job is not None:
                    job.state = 'queued'
                    job.next_attempt_at = None
                    job.failure_reason = None
                now = datetime.now(UTC)
                record_event(
                    session,
                    record.id,
                    'destination_conflict_cleaned',
                    'queued' if queued else 'needs_review',
                    'unmanaged destination removed by review action',
                    now,
                    source.id,
                )
                session.commit()
                return DestinationConflictCleanupResponse(
                    record_id=record.id,
                    source_id=source.id,
                    path=str(path),
                    removed=True,
                    queued=queued,
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.post(
        '/api/library/records/{record_id}/sources/{source_id}/destination-conflict/replace',
        response_model=DestinationConflictCleanupResponse,
    )
    def replace_destination_conflict(record_id: str, source_id: str) -> DestinationConflictCleanupResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                conflict = destination_conflict(session, record, source)
                if conflict is None:
                    raise HTTPException(status_code=404, detail='destination conflict not found')
                if conflict['ownership'] != 'managed':
                    raise HTTPException(status_code=409, detail='only service-owned destinations can be replaced')
                path = Path(conflict['path'])
                if media_root is None or path == media_root.resolve() or media_root.resolve() not in path.parents:
                    raise HTTPException(status_code=409, detail='destination is outside the media root')
                if path.is_dir():
                    shutil.rmtree(path)
                else:
                    path.unlink()
                now = datetime.now(UTC)
                kind = queue_source_recovery(session, record, source, now)
                record_event(
                    session,
                    record.id,
                    'destination_replaced',
                    'queued' if kind is not None else 'needs_review',
                    'service-owned destination removed and recovery requested',
                    now,
                    source.id,
                )
                session.commit()
                return DestinationConflictCleanupResponse(
                    record_id=record.id,
                    source_id=source.id,
                    path=str(path),
                    removed=True,
                    queued=kind is not None,
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

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
    def retry_provider_for_source(
        record_id: str,
        source_id: str,
        request: ProviderRetryRequest = _DEFAULT_PROVIDER_RETRY_REQUEST,
    ) -> ProviderRetryResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                if source.disappeared_at is not None:
                    return ProviderRetryResponse(source_id=source.id, queued=False)
                now = datetime.now(UTC)
                queued = (
                    JobRepository(session).requeue_provider(source.id, request.provider, now)
                    if request.provider is not None
                    else JobRepository(session).requeue_source(source.id, now)
                )
                if queued:
                    record_event(
                        session,
                        record.id,
                        'provider_retry_queued',
                        'queued',
                        f'{request.provider or "all providers"} retry requested from review UI',
                        now,
                        source.id,
                    )
                session.commit()
                return ProviderRetryResponse(source_id=source.id, queued=queued)
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.get('/api/library/records/{record_id}/sources/{source_id}/candidates/{candidate_key}/musicbrainz')
    def decode_acoustid_candidate(record_id: str, source_id: str, candidate_key: str) -> JSONResponse:
        if musicbrainz_provider is None:
            raise HTTPException(status_code=503, detail='MusicBrainz provider is not configured')
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                candidate = next(
                    (item for item in reversed(source.candidates) if item.candidate_key == candidate_key),
                    None,
                )
                if candidate is None:
                    raise LookupError(candidate_key)
                evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
                if evidence.provider != 'acoustid':
                    raise HTTPException(status_code=409, detail='candidate is not an AcousticID recording')
                source_tags = _catalog_tags(record, source.id)
                result = ProviderEvidenceService(session, musicbrainz_provider, None).lookup(
                    ProviderEvidenceRequest(
                        query='',
                        musicbrainz_case=FixtureCase.SUCCESS,
                        fingerprint=None,
                        acoustid_case=None,
                        force_refresh=True,
                        release_title=source_tags.get('ALBUM'),
                        artist_name=source_tags.get('ARTIST'),
                        recording_mbid=candidate_key,
                        run_acoustid=False,
                        run_musicbrainz=True,
                    ),
                    datetime.now(UTC),
                )
                if isinstance(result.musicbrainz, MusicBrainzMatch):
                    metadata = result.musicbrainz.candidate
                elif isinstance(result.musicbrainz, Ambiguous):
                    source_album = source_tags.get('ALBUM', '').casefold()
                    metadata = next(
                        (
                            candidate
                            for candidate in result.musicbrainz.candidates
                            if candidate.release_title.casefold() == source_album
                        ),
                        result.musicbrainz.candidates[0] if result.musicbrainz.candidates else None,
                    )
                else:
                    metadata = None
                if metadata is None:
                    return JSONResponse(
                        content={
                            'recording_mbid': candidate_key,
                            'artist': source_tags.get('ARTIST', ''),
                            'title': source_tags.get('TITLE', ''),
                            'album': source_tags.get('ALBUM', ''),
                            'release_mbid': None,
                            'resolved': False,
                            'tags': {},
                        }
                    )
                return JSONResponse(
                    content={
                        'recording_mbid': candidate_key,
                        'artist': metadata.artist_name or source_tags.get('ARTIST', ''),
                        'title': metadata.recording_title or source_tags.get('TITLE', ''),
                        'album': metadata.release_title or source_tags.get('ALBUM', ''),
                        'release_mbid': metadata.release_mbid,
                        'resolved': True,
                        'tags': {
                            name: value
                            for name, value in {
                                'TITLE': metadata.recording_title,
                                'ARTIST': metadata.artist_name,
                                'ALBUM': metadata.release_title,
                                'MUSICBRAINZ_TRACKID': candidate_key,
                                'MUSICBRAINZ_ALBUMID': metadata.release_mbid,
                            }.items()
                            if value is not None
                        },
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record, source, or candidate not found') from error

    @app.post('/api/library/records/{record_id}/sources/{source_id}/candidates/select')
    def select_provider_candidate(record_id: str, source_id: str, request: CandidateSelection) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                candidate = next(
                    (item for item in reversed(source.candidates) if item.candidate_key == request.candidate_key),
                    None,
                )
                if candidate is None:
                    raise HTTPException(status_code=404, detail='provider candidate not found')
                evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
                if evidence.provider != request.provider:
                    raise HTTPException(status_code=409, detail='candidate belongs to another provider')
                now = datetime.now(UTC)
                if request.provider == 'acoustid':
                    record.musicbrainz_recording_id = candidate.candidate_key
                    record.musicbrainz_release_id = None
                    queued = JobRepository(session).requeue_provider(source.id, 'musicbrainz', now)
                    session.add(
                        ReviewDecisionRecord(
                            source_id=source.id,
                            state='acoustid_confirmed',
                            rationale=f'AcousticID recording {candidate.candidate_key} selected by reviewer',
                        )
                    )
                    record_event(
                        session,
                        record.id,
                        'acoustid_candidate_confirmed',
                        'analyzing',
                        f'AcousticID recording {candidate.candidate_key} selected; MusicBrainz queued',
                        now,
                        source.id,
                    )
                    session.commit()
                    return JSONResponse(
                        content={'candidate_key': candidate.candidate_key, 'revision': None, 'queued': queued}
                    )
                candidate_tags = evidence.tags
                if not candidate_tags:
                    raise HTTPException(status_code=409, detail='provider candidate has no metadata')
                source_tags = _catalog_tags(record, source.id)
                final_tags = {**source_tags, **candidate_tags}
                analyzed = append_metadata_revision(
                    session, record.id, source.id, 'analyzed', candidate_tags, 'review', now
                )
                final = append_metadata_revision(session, record.id, source.id, 'final', final_tags, 'review', now)
                record.match_state = 'matched'
                record.musicbrainz_release_id = candidate.candidate_key
                record.musicbrainz_recording_id = candidate_tags.get('MUSICBRAINZ_TRACKID')
                session.add(
                    ReviewDecisionRecord(
                        source_id=source.id,
                        state='confirmed',
                        rationale=f'provider candidate {candidate.candidate_key} selected by reviewer',
                    )
                )
                queued = JobRepository(session).enqueue(source.id, 'final_publish', now, final.id)
                record_event(
                    session,
                    record.id,
                    'provider_candidate_confirmed',
                    'publishing',
                    f'provider candidate {candidate.candidate_key} selected; final revision {analyzed.revision}',
                    now,
                    source.id,
                )
                session.commit()
                return JSONResponse(
                    content={
                        'candidate_key': candidate.candidate_key,
                        'revision': final.revision,
                        'queued': queued is not None,
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.post('/api/library/records/{record_id}/sources/{source_id}/musicbrainz/override')
    def override_musicbrainz_release(record_id: str, source_id: str, request: MusicBrainzOverride) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                now = datetime.now(UTC)
                record.musicbrainz_release_id = request.release_mbid.lower()
                queued = JobRepository(session).requeue_provider(source.id, 'musicbrainz', now)
                record_event(
                    session,
                    record.id,
                    'musicbrainz_release_override_queued',
                    'analyzing',
                    f'MusicBrainz release {request.release_mbid} explicitly selected by reviewer',
                    now,
                    source.id,
                )
                session.commit()
                return JSONResponse(content={'release_mbid': record.musicbrainz_release_id, 'queued': queued})
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
