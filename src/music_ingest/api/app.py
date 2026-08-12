from __future__ import annotations

import json
import shutil
from datetime import UTC, datetime
from pathlib import Path
from typing import Protocol

from fastapi import FastAPI, HTTPException, Request, Response, status
from fastapi.responses import HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from sqlalchemy import select
from sqlalchemy.orm import Session
from starlette.types import Lifespan

from music_ingest.api.lidarr_intake import LidarrIntakeError, dispatch_lidarr_event, parse_lidarr_event
from music_ingest.association import (
    ManualAssociationRequest,
    RecordingAssociationService,
    RecordingAssociationUnavailable,
)
from music_ingest.dto import (
    CandidateEvidencePayload,
    CandidateSelection,
    DestinationConflictCleanupResponse,
    FullReprocessResponse,
    GenreCatalogItemResponse,
    GenreCatalogResponse,
    LibraryIdentityUpdate,
    ManualSourceSelection,
    MatchingSettings,
    MetadataUpdate,
    MusicBrainzOverride,
    ProviderRetryRequest,
    ProviderRetryResponse,
    ProviderRetryResult,
    RecoveryResponse,
    RuntimeSettingsRequest,
    RuntimeSettingsResponse,
    SourceRecoveryResponse,
    SourceRootCandidateListResponse,
    SourceRootCandidateResponse,
    SourceRootCreateRequest,
    SourceRootListResponse,
    SourceRootResponse,
    SourceRootUpdateRequest,
    StorageBrowserItemResponse,
    StorageBrowserResponse,
    StorageConfigResponse,
    StorageOutputPreviewResponse,
    StoragePathRequest,
)
from music_ingest.external.musicbrainz import MusicBrainzTransport, MusicBrainzV2Adapter
from music_ingest.external.musicbrainz_genres import (
    GenreCatalogSyncError,
    GenreTransport,
    load_genre_catalog,
    replace_genre_catalog,
    sync_genres,
)
from music_ingest.library.service import (
    append_metadata_revision,
    attach_source,
    library_record_detail,
    library_records,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.matching.providers import Ambiguous, FixtureCase, MusicBrainzMatch, MusicBrainzProvider
from music_ingest.models import (
    CandidateRecord,
    GenreCatalogRecord,
    JobRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
    SourceTagRecord,
)
from music_ingest.models.jobs import JobRepository
from music_ingest.models.library import SourceRecordView
from music_ingest.models.repositories import ReceiptReplayConflictError
from music_ingest.reconciliation import ScanResult, mark_disappeared_source, reconcile_incoming
from music_ingest.settings import RuntimeSettings, load_runtime_settings, save_runtime_settings
from music_ingest.source_boundary import SourceBoundaryError, resolve_owned_source
from music_ingest.source_roots import (
    SourceRootConflictError,
    SourceRootService,
    SourceRootValidationError,
)
from music_ingest.storage import StorageService, StorageValidationError
from music_ingest.ui.page import REVIEW_PAGE

_E2E_RECORD_ID = 'e2e-record'
_E2E_SOURCE_IDS = ('e2e-source-a', 'e2e-source-b')
_E2E_RECORDING_MBID = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
_E2E_CORRECTION_MBID = '11111111-1111-4111-8111-111111111111'


class SessionFactory(Protocol):
    def __call__(self) -> Session: ...


def _settings_response(settings: RuntimeSettings) -> RuntimeSettingsResponse:
    return RuntimeSettingsResponse(
        confidence_threshold=settings.confidence_threshold,
        timeout_seconds=settings.timeout_seconds,
        retry_delay_seconds=settings.retry_delay_seconds,
        max_attempts=settings.max_attempts,
        musicbrainz_enabled=settings.musicbrainz_enabled,
        musicbrainz_user_agent=settings.musicbrainz_user_agent,
        acoustid_enabled=settings.acoustid_enabled,
        acoustid_client_key_configured=bool(settings.acoustid_client_key),
        artwork_enabled=settings.artwork_enabled,
    )


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


_DEFAULT_PROVIDER_RETRY_REQUEST = ProviderRetryRequest()


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
    if not record.sources:
        return ('', '', 2**31 - 1, '', record.id, '')
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


def _needs_analysis_retry(source: SourceRecordView) -> bool:
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
    source_roots_parent: Path | None = None,
    media_root: Path | None = None,
    e2e_seed_enabled: bool = False,
    musicbrainz_provider: MusicBrainzProvider | None = None,
    musicbrainz_transport: MusicBrainzTransport | None = None,
    genre_transport: GenreTransport | None = None,
    storage_browse_roots: tuple[Path, ...] | None = None,
) -> FastAPI:
    app = FastAPI(title='Music ingestion review', version='0.1.0', lifespan=lifespan)
    app.state.e2e_seed_enabled = e2e_seed_enabled
    assets_root = Path(__file__).parents[1] / 'ui' / 'dist' / 'assets'
    if assets_root.is_dir():
        app.mount('/assets', StaticFiles(directory=assets_root), name='ui-assets')

    @app.get('/healthz')
    def healthz() -> dict[str, str]:
        return {'status': 'ok', 'service': 'music-ingest'}

    @app.get('/', response_class=HTMLResponse)
    @app.get('/review', response_class=HTMLResponse)
    @app.get('/settings', response_class=HTMLResponse)
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

    @app.post('/api/e2e/seed')
    def seed_e2e_fixtures() -> JSONResponse:
        if not app.state.e2e_seed_enabled:
            raise HTTPException(status_code=404, detail='not found')
        now = datetime.now(UTC)
        with session_factory() as session:
            root = session.scalar(select(SourceRootRecord).where(SourceRootRecord.id == 'legacy'))
            if root is None:
                root = SourceRootRecord(
                    id='legacy',
                    display_name='legacy',
                    canonical_path=str(incoming_root),
                    enabled=True,
                    scan_state='never_scanned',
                    created_at=now,
                    updated_at=now,
                )
                session.add(root)
            record = session.get(LibraryRecord, _E2E_RECORD_ID)
            if record is None:
                record = LibraryRecord(id=_E2E_RECORD_ID, created_at=now, updated_at=now, match_state='matched')
                session.add(record)
            for index, source_id in enumerate(_E2E_SOURCE_IDS):
                source = session.get(SourceRecord, source_id)
                if source is None:
                    source = SourceRecord(
                        id=source_id,
                        source_path=f'/data/sources/legacy/e2e/{source_id}.flac',
                        device=1,
                        inode=index + 1,
                        size_bytes=1,
                        sha256=f'{index + 1:064x}',
                        duration_seconds=180,
                        origin='e2e',
                        intake_state='present',
                        source_root=root,
                        library_record=record,
                        tag_observations=[
                            SourceTagRecord(format_name='e2e', tag_name='ARTIST', value='Fixture Artist'),
                            SourceTagRecord(format_name='e2e', tag_name='ALBUM', value='Fixture Album'),
                            SourceTagRecord(format_name='e2e', tag_name='TITLE', value='Fixture Track'),
                            SourceTagRecord(format_name='e2e', tag_name='TRACKNUMBER', value='1'),
                        ],
                        provider_attempts=[
                            ProviderAttemptRecord(
                                provider_name='musicbrainz',
                                outcome='musicbrainzmatch',
                                snapshot_sha256='e2e-provider-snapshot',
                                snapshot='e2e fixture provider evidence',
                            )
                        ],
                        candidates=[
                            CandidateRecord(
                                candidate_key='e2e-release',
                                evidence=json.dumps(
                                    {
                                        'provider': 'musicbrainz',
                                        'artist': 'Fixture Artist',
                                        'release': 'Fixture Album',
                                        'score': 1.0,
                                        'tags': {
                                            'MUSICBRAINZ_ALBUMID': '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c',
                                            'MUSICBRAINZ_TRACKID': _E2E_RECORDING_MBID,
                                        },
                                    },
                                    sort_keys=True,
                                ),
                            )
                        ],
                    )
                    session.add(source)
            record.musicbrainz_recording_id = _E2E_RECORDING_MBID
            record.match_state = 'matched'
            session.commit()
        return JSONResponse(
            content={
                'record_id': _E2E_RECORD_ID,
                'source_ids': list(_E2E_SOURCE_IDS),
                'recording_mbid': _E2E_RECORDING_MBID,
                'correction_mbid': _E2E_CORRECTION_MBID,
            }
        )

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
            result = reconcile_incoming(session)
            session.commit()
            return result

    @app.post('/api/library/reprocess-all', response_model=FullReprocessResponse)
    def reprocess_all_library() -> FullReprocessResponse:
        now = datetime.now(UTC)
        with session_factory() as session:
            queued = 0
            jobs = JobRepository(session)
            for record in library_records(session):
                for source in record.sources:
                    if source.disappeared_at is not None:
                        continue
                    persisted_source = SourceRecord.get(session, source.id)
                    if persisted_source is not None and not Path(persisted_source.source_path).is_file():
                        mark_disappeared_source(session, persisted_source)
                        continue
                    root = None if persisted_source is None else persisted_source.source_root
                    if root is not None and (
                        not root.enabled
                        or root.id == 'historical-unmanaged'
                        or root.canonical_path.startswith('historical-unmanaged://')
                    ):
                        continue
                    _ = require_owned_source(session, source.id)
                    if jobs.enqueue(source.id, 'filesystem_scan', now) is not None:
                        queued += 1
            session.commit()
        return FullReprocessResponse(queued=queued)

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
                .where(
                    JobRecord.source_id == source.id,
                    JobRecord.kind.not_in(['acoustid_analysis', 'musicbrainz_analysis', 'final_publish']),
                )
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
        _ = require_owned_source(session, source.id)
        if source.disappeared_at is not None:
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
            kind = 'acoustid_analysis'
        elif current_publication is None:
            kind = 'final_publish' if final_revision is not None else 'filesystem_scan'
        elif record.processing_state == 'publishing' or record.publication_state in {'stale', 'failed'}:
            kind = 'final_publish'
        elif record.processing_state != 'complete':
            kind = 'acoustid_analysis'
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

    def require_owned_source(session: Session, source_id: str) -> SourceRecord:
        persisted_source = session.get(SourceRecord, source_id)
        if persisted_source is None:
            raise HTTPException(status_code=404, detail='source not found')
        try:
            _ = resolve_owned_source(persisted_source)
        except SourceBoundaryError as error:
            raise HTTPException(status_code=409, detail=f'source root boundary: {error}') from error
        return persisted_source

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
                _ = require_owned_source(session, source.id)
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
                        not request.retry_all and not _needs_analysis_retry(source)
                    ):
                        continue
                    _ = require_owned_source(session, source.id)
                    provider_queued = (
                        jobs.requeue_provider(source.id, request.provider, now)
                        if request.provider is not None
                        else jobs.requeue_source(source.id, now)
                    )
                    if provider_queued:
                        record_event(
                            session,
                            record.id,
                            'analysis_retry_queued',
                            'queued',
                            f'{request.provider or "all analysis stages"} retry requested from review UI',
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
                _ = require_owned_source(session, source.id)
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
                        JobRecord.source_id == source.id,
                        JobRecord.kind.not_in(['acoustid_analysis', 'musicbrainz_analysis', 'final_publish']),
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
                _ = require_owned_source(session, source.id)
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
                _ = require_owned_source(session, source.id)
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
                        'analysis_retry_queued',
                        'queued',
                        f'{request.provider or "all analysis stages"} retry requested from review UI',
                        now,
                        source.id,
                    )
                session.commit()
                return ProviderRetryResponse(source_id=source.id, queued=queued)
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @app.get('/api/library/records/{record_id}/sources/{source_id}/candidates/{candidate_key}/musicbrainz')
    def decode_acoustid_candidate(record_id: str, source_id: str, candidate_key: str) -> JSONResponse:
        try:
            with session_factory() as session:
                provider = musicbrainz_provider
                if provider is None and musicbrainz_transport is not None:
                    provider = MusicBrainzV2Adapter(
                        musicbrainz_transport,
                        load_runtime_settings(session).musicbrainz_user_agent,
                    )
                if provider is None:
                    raise HTTPException(status_code=503, detail='MusicBrainz provider is not configured')
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
                if evidence.releases:
                    source_album = source_tags.get('ALBUM', '').casefold()
                    metadata = next(
                        (release for release in evidence.releases if release.album.casefold() == source_album),
                        evidence.releases[0],
                    )
                    return JSONResponse(
                        content={
                            'recording_mbid': candidate_key,
                            'artist': metadata.artist or source_tags.get('ARTIST', ''),
                            'title': metadata.title or source_tags.get('TITLE', ''),
                            'album': metadata.album or source_tags.get('ALBUM', ''),
                            'release_mbid': metadata.release_mbid,
                            'resolved': True,
                            'tags': metadata.tags,
                        }
                    )
                result = ProviderEvidenceService(session, provider, None).lookup(
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
                _ = require_owned_source(session, source.id)
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
                    session.flush()
                    session.expire_all()
                    _ = reevaluate_effective_source_decision(session, record.id, now)
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
                session.flush()
                session.expire_all()
                _ = reevaluate_effective_source_decision(session, record.id, now)
                queued = JobRepository(session).enqueue_selection_refresh(record.id, now)
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

    @app.post('/api/library/records/{record_id}/effective-source')
    def select_effective_source(record_id: str, request: ManualSourceSelection) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                decision = reevaluate_effective_source_decision(
                    session, record.id, datetime.now(UTC), manual_source_id=request.source_id
                )
                _ = JobRepository(session).enqueue_selection_refresh(record.id, datetime.now(UTC))
                session.commit()
                return JSONResponse(
                    content={
                        'source_id': decision.source_id,
                        'baseline_source_id': decision.baseline_source_id,
                        'policy_version': decision.policy_version,
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error
        except ValueError as error:
            raise HTTPException(status_code=409, detail=str(error)) from error

    @app.post('/api/library/records/{record_id}/sources/{source_id}/musicbrainz/override')
    def override_musicbrainz_release(record_id: str, source_id: str, request: MusicBrainzOverride) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                now = datetime.now(UTC)
                provider = musicbrainz_provider
                if provider is None and musicbrainz_transport is not None:
                    provider = MusicBrainzV2Adapter(
                        musicbrainz_transport, load_runtime_settings(session).musicbrainz_user_agent
                    )
                result = RecordingAssociationService(session, provider).associate_manual(
                    ManualAssociationRequest(
                        source.id,
                        request.recording_mbid.lower(),
                        now,
                    )
                )
                session.commit()
                return JSONResponse(
                    content={'recording_mbid': request.recording_mbid.lower(), 'record_id': result.library_record_id}
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error
        except RecordingAssociationUnavailable as error:
            raise HTTPException(status_code=503, detail=str(error)) from error

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
                _ = require_owned_source(session, source.id)
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
                queued = JobRepository(session).enqueue_selection_refresh(record.id, now)
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

    @app.get('/api/settings', response_model=RuntimeSettingsResponse)
    def runtime_settings() -> RuntimeSettingsResponse:
        with session_factory() as session:
            return _settings_response(load_runtime_settings(session))

    def source_root_service(session: Session) -> SourceRootService:
        return SourceRootService(session, source_roots_parent if media_root is None else None)

    def storage_service(session: Session) -> StorageService:
        if media_root is None:
            raise HTTPException(status_code=503, detail='storage administration is not configured')
        browse_roots = storage_browse_roots or tuple(
            root for root in (source_roots_parent, incoming_root.parent, media_root.parent) if root is not None
        )
        return StorageService(session, browse_roots, media_root)

    def source_root_response(root: SourceRootRecord) -> SourceRootResponse:
        return SourceRootResponse(
            id=root.id,
            display_name=root.display_name,
            canonical_path=root.canonical_path,
            enabled=root.enabled,
            scan_state=root.scan_state,
        )

    @app.get('/api/settings/source-roots', response_model=SourceRootListResponse)
    def list_source_roots() -> SourceRootListResponse:
        try:
            with session_factory() as session:
                service = source_root_service(session)
                return SourceRootListResponse(items=tuple(source_root_response(root) for root in service.list()))
        except SourceRootValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.get('/api/settings/source-roots/candidates', response_model=SourceRootCandidateListResponse)
    def list_source_root_candidates() -> SourceRootCandidateListResponse:
        try:
            with session_factory() as session:
                candidates = source_root_service(session).candidates()
                return SourceRootCandidateListResponse(
                    items=tuple(
                        SourceRootCandidateResponse(name=candidate.name, canonical_path=str(candidate))
                        for candidate in candidates
                    )
                )
        except SourceRootValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.get('/api/settings/storage/browser', response_model=StorageBrowserResponse)
    def browse_storage(path: str | None = None) -> StorageBrowserResponse:
        try:
            with session_factory() as session:
                browser = storage_service(session).browse(path)
                return StorageBrowserResponse(
                    path=str(browser.path),
                    parent_path=None if browser.parent_path is None else str(browser.parent_path),
                    items=tuple(StorageBrowserItemResponse(name=item.name, path=str(item)) for item in browser.items),
                )
        except StorageValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.get('/api/settings/storage', response_model=StorageConfigResponse)
    def storage_config() -> StorageConfigResponse:
        with session_factory() as session:
            config = storage_service(session).config()
            session.commit()
            return StorageConfigResponse(
                output_root=config.output_root,
                state=config.state,
                generation=config.generation,
            )

    @app.post('/api/settings/storage/output/preview', response_model=StorageOutputPreviewResponse)
    def preview_storage_output(request: StoragePathRequest) -> StorageOutputPreviewResponse:
        try:
            with session_factory() as session:
                preview = storage_service(session).preview_output(request.path)
                return StorageOutputPreviewResponse(
                    output_root=str(preview.output_root),
                    same_filesystem=preview.same_filesystem,
                    file_count=preview.file_count,
                )
        except StorageValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.put('/api/settings/storage/output', response_model=StorageConfigResponse)
    def move_storage_output(request: StoragePathRequest) -> StorageConfigResponse:
        try:
            with session_factory() as session:
                config = storage_service(session).move_output(request.path)
                session.commit()
                return StorageConfigResponse(
                    output_root=config.output_root,
                    state=config.state,
                    generation=config.generation,
                )
        except StorageValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.post('/api/settings/source-roots', response_model=SourceRootResponse, status_code=status.HTTP_201_CREATED)
    def create_source_root(request: SourceRootCreateRequest) -> SourceRootResponse:
        try:
            with session_factory() as session:
                service = source_root_service(session)
                if media_root is not None:
                    _ = storage_service(session).validate_input(request.path)
                root = service.create(request.path, request.display_name)
                session.commit()
                return source_root_response(root)
        except SourceRootValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error
        except SourceRootConflictError as error:
            raise HTTPException(status_code=409, detail=str(error)) from error
        except StorageValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.delete('/api/settings/source-roots/{root_id}', status_code=status.HTTP_204_NO_CONTENT)
    def delete_source_root(root_id: str) -> Response:
        with session_factory() as session:
            if not source_root_service(session).remove(root_id):
                raise HTTPException(status_code=404, detail='source root not found')
            session.commit()
            return Response(status_code=status.HTTP_204_NO_CONTENT)

    @app.put('/api/settings/source-roots/{root_id}', response_model=SourceRootResponse)
    def update_source_root(root_id: str, request: SourceRootUpdateRequest) -> SourceRootResponse:
        try:
            with session_factory() as session:
                root = source_root_service(session).update(root_id, request.display_name, request.enabled)
                if root is None:
                    raise HTTPException(status_code=404, detail='source root not found')
                session.commit()
                return source_root_response(root)
        except SourceRootValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @app.put('/api/settings', response_model=RuntimeSettingsResponse)
    def update_runtime_settings(request: RuntimeSettingsRequest) -> RuntimeSettingsResponse:
        with session_factory() as session:
            current = load_runtime_settings(session)
            settings = RuntimeSettings(
                confidence_threshold=request.confidence_threshold,
                timeout_seconds=request.timeout_seconds,
                retry_delay_seconds=request.retry_delay_seconds,
                max_attempts=request.max_attempts,
                musicbrainz_enabled=request.musicbrainz_enabled,
                musicbrainz_user_agent=request.musicbrainz_user_agent,
                acoustid_enabled=request.acoustid_enabled,
                acoustid_client_key=(
                    current.acoustid_client_key if request.acoustid_client_key is None else request.acoustid_client_key
                ),
                artwork_enabled=request.artwork_enabled,
            )
            if settings.acoustid_enabled and not settings.acoustid_client_key:
                raise HTTPException(status_code=422, detail='AcoustID requires a client key when enabled')
            save_runtime_settings(session, settings)
            session.commit()
            return _settings_response(settings)

    @app.get('/api/genres', response_model=GenreCatalogResponse)
    def genre_catalog() -> GenreCatalogResponse:
        with session_factory() as session:
            entries = load_genre_catalog(session)
            if not entries and genre_transport is not None:
                settings = load_runtime_settings(session)
                try:
                    synced_entries = sync_genres(genre_transport, user_agent=settings.musicbrainz_user_agent)
                except GenreCatalogSyncError:
                    return _genre_catalog_response(entries)
                replace_genre_catalog(session, synced_entries, datetime.now(UTC))
                session.commit()
                entries = load_genre_catalog(session)
            return _genre_catalog_response(entries)

    @app.post('/api/genres/sync', response_model=GenreCatalogResponse)
    def sync_genre_catalog() -> GenreCatalogResponse:
        if genre_transport is None:
            raise HTTPException(status_code=503, detail='MusicBrainz genre sync is unavailable')
        with session_factory() as session:
            settings = load_runtime_settings(session)
            try:
                entries = sync_genres(genre_transport, user_agent=settings.musicbrainz_user_agent)
            except GenreCatalogSyncError as error:
                raise HTTPException(status_code=502, detail=str(error)) from error
            synced_at = datetime.now(UTC)
            replace_genre_catalog(session, entries, synced_at)
            session.commit()
            return _genre_catalog_response(load_genre_catalog(session))

    @app.get('/api/settings/matching', response_model=MatchingSettings)
    def matching_settings() -> MatchingSettings:
        with session_factory() as session:
            return MatchingSettings(confidence_threshold=load_runtime_settings(session).confidence_threshold)

    @app.put('/api/settings/matching', response_model=MatchingSettings)
    def update_matching_settings(request: MatchingSettings) -> MatchingSettings:
        with session_factory() as session:
            current = load_runtime_settings(session)
            save_runtime_settings(
                session,
                current.model_copy(update={'confidence_threshold': request.confidence_threshold}),
            )
            session.commit()
            return request

    return app
