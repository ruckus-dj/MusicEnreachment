from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi import APIRouter, HTTPException
from sqlalchemy import and_, or_, select
from sqlalchemy.orm import Session

from music_ingest.api.dependencies import SessionFactory
from music_ingest.api.library_access import (
    destination_conflict,
    queue_record_recovery,
    queue_source_recovery,
    require_owned_source,
)
from music_ingest.contracts import (
    DestinationConflictCleanupResponse,
    FullReprocessResponse,
    ProviderRetryRequest,
    ProviderRetryResponse,
    ProviderRetryResult,
    RecoveryResponse,
    SourceRecoveryResponse,
)
from music_ingest.models import (
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
    PublicationAttemptRecord,
    SourceRecord,
    SourceRootRecord,
    StorageConfigRecord,
)
from music_ingest.models.library import SourceRecordView
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import (
    library_record_detail,
    library_records,
    record_event,
)
from music_ingest.services.musicbrainz_identity import ConfirmedMusicBrainzIdentity
from music_ingest.services.publication.locks import acquire_storage_lock
from music_ingest.services.reconciliation import mark_disappeared_source
from music_ingest.services.source_boundary import SourceBoundaryError, resolve_regular_file

_DEFAULT_PROVIDER_RETRY_REQUEST = ProviderRetryRequest()


_RETRYABLE_PROVIDER_OUTCOMES = frozenset(
    {'malformed', 'rate_limited', 'ratelimited', 'timeout', 'unavailable', 'disabled', 'failed'}
)


def _needs_analysis_retry(source: SourceRecordView) -> bool:
    return not source.provider_attempts or any(
        attempt.outcome.casefold() in _RETRYABLE_PROVIDER_OUTCOMES for attempt in source.provider_attempts
    )


def _remove_conflict_path(path: Path) -> None:
    if path.suffix.lower() == '.nfo':
        return
    if path.is_symlink() or not path.is_dir():
        path.unlink()
        return
    for child in tuple(path.iterdir()):
        _remove_conflict_path(child)
    if not any(path.iterdir()):
        path.rmdir()


def create_router(session_factory: SessionFactory, *, media_root: Path | None = None) -> APIRouter:
    router = APIRouter()

    def writable_media_root(session: Session) -> Path | None:
        acquire_storage_lock(session)
        config = session.get(StorageConfigRecord, 1, populate_existing=True)
        if config is not None and config.state != 'ready':
            raise HTTPException(status_code=409, detail='output migration is active; retry after completion')
        if (
            session.scalar(select(PublicationAttemptRecord.id).where(PublicationAttemptRecord.cleaned_at.is_(None)))
            is not None
        ):
            raise HTTPException(status_code=409, detail='publication recovery is active; retry after completion')
        return media_root if config is None else Path(config.output_root)

    @router.post('/api/library/reprocess-all', response_model=FullReprocessResponse)
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

    @router.post('/api/library/metadata/refresh', response_model=FullReprocessResponse)
    def refresh_library_metadata() -> FullReprocessResponse:
        now = datetime.now(UTC)
        with session_factory() as session:
            seen_record_ids: set[str] = set()
            eligible_sources: list[tuple[str, ConfirmedMusicBrainzIdentity]] = []
            rows = session.execute(
                select(
                    LibraryRecord.id,
                    LibraryRecord.musicbrainz_recording_id,
                    LibraryRecord.musicbrainz_release_id,
                    SourceRecord.id,
                    SourceRecord.source_path,
                    SourceRecord.disappeared_at,
                    SourceRecord.intake_state,
                    SourceRootRecord.id,
                    SourceRootRecord.canonical_path,
                    SourceRootRecord.enabled,
                    LibraryPublicationRecord.id,
                )
                .join(SourceRecord, SourceRecord.library_record_id == LibraryRecord.id)
                .join(SourceRootRecord, SourceRootRecord.id == SourceRecord.source_root_id)
                .outerjoin(
                    LibraryPublicationRecord,
                    and_(
                        LibraryPublicationRecord.library_record_id == LibraryRecord.id,
                        LibraryPublicationRecord.state == 'current',
                    ),
                )
                .where(~LibraryRecord.id.in_(select(LibraryRecordConsolidationRecord.retired_library_record_id)))
                .where(LibraryRecord.musicbrainz_recording_id.is_not(None))
                .where(LibraryRecord.musicbrainz_recording_id != '')
                .where(LibraryRecord.musicbrainz_release_id.is_not(None))
                .where(LibraryRecord.musicbrainz_release_id != '')
                .where(LibraryRecord.match_state == 'matched')
                .where(
                    or_(
                        LibraryPublicationRecord.source_id == SourceRecord.id,
                        and_(
                            LibraryPublicationRecord.id.is_(None),
                            SourceRecord.disappeared_at.is_(None),
                            SourceRecord.intake_state.not_in({'replaced', 'quarantined'}),
                        ),
                    )
                )
                .order_by(LibraryRecord.id, SourceRecord.id)
            ).tuples()
            for (
                record_id,
                recording_mbid,
                release_mbid,
                source_id,
                source_path,
                disappeared_at,
                _intake_state,
                root_id,
                root_path,
                root_enabled,
                _publication_id,
            ) in rows:
                if record_id in seen_record_ids:
                    continue
                seen_record_ids.add(record_id)
                if disappeared_at is not None:
                    continue
                path = Path(source_path)
                if not path.is_file():
                    persisted_source = SourceRecord.get(session, source_id)
                    if persisted_source is not None:
                        mark_disappeared_source(session, persisted_source)
                    continue
                if (
                    not root_enabled
                    or root_id == 'historical-unmanaged'
                    or root_path.startswith('historical-unmanaged://')
                ):
                    continue
                try:
                    _ = resolve_regular_file(path, Path(root_path))
                except SourceBoundaryError as error:
                    raise HTTPException(status_code=409, detail=f'source root boundary: {error}') from error
                if recording_mbid is None or release_mbid is None:
                    continue
                eligible_sources.append((source_id, ConfirmedMusicBrainzIdentity(recording_mbid, release_mbid)))
            jobs = JobRepository(session)
            queued = sum(
                jobs.enqueue_musicbrainz_refresh(source_id, identity, now) is not None
                for source_id, identity in eligible_sources
            )
            session.commit()
        return FullReprocessResponse(queued=queued)

    @router.post('/api/library/artwork/reprocess', response_model=FullReprocessResponse)
    def reprocess_library_artwork() -> FullReprocessResponse:
        now = datetime.now(UTC)
        with session_factory() as session:
            queued = 0
            release_mbids = {
                record.musicbrainz_release_id.strip()
                for record in library_records(session)
                if record.musicbrainz_release_id is not None and record.musicbrainz_release_id.strip()
            }
            jobs = JobRepository(session)
            for release_mbid in release_mbids:
                if jobs.enqueue_release_artwork(release_mbid, now) is not None:
                    queued += 1
            session.commit()
        return FullReprocessResponse(queued=queued)

    @router.post('/api/library/recovery', response_model=RecoveryResponse)
    def recover_library() -> RecoveryResponse:
        now = datetime.now(UTC)
        queued = skipped = conflicts = 0
        with session_factory() as session:
            for record in library_records(session):
                record_queued, record_conflicts = queue_record_recovery(session, record, now, media_root)
                queued += record_queued
                conflicts += record_conflicts
                if record_queued == 0 and record_conflicts == 0:
                    skipped += 1
            session.commit()
        return RecoveryResponse(queued=queued, skipped=skipped, conflicts=conflicts)

    @router.post(
        '/api/library/records/{record_id}/sources/{source_id}/reprocess', response_model=SourceRecoveryResponse
    )
    def reprocess_source(record_id: str, source_id: str) -> SourceRecoveryResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                conflict = destination_conflict(session, record, source, media_root)
                if conflict is not None:
                    raise HTTPException(status_code=409, detail='destination conflict must be replaced first')
                now = datetime.now(UTC)
                kind = queue_source_recovery(session, record, source, now)
                replacement = None
                if source.intake_state == 'replaced':
                    replacement = (
                        session.get(SourceRecord, source.replaced_by_source_id)
                        if source.replaced_by_source_id is not None
                        else session.scalar(
                            select(SourceRecord)
                            .where(SourceRecord.source_root_id == source.source_root_id)
                            .where(SourceRecord.source_path == source.source_path)
                            .where(SourceRecord.intake_state != 'replaced')
                            .order_by(SourceRecord.id.desc())
                        )
                    )
                session.commit()
                return SourceRecoveryResponse(
                    record_id=record.id,
                    source_id=source.id,
                    queued=kind is not None,
                    kind=kind,
                    replacement_record_id=replacement.library_record_id if replacement is not None else None,
                    replacement_source_id=replacement.id if replacement is not None else None,
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @router.post('/api/library/providers/retry', response_model=ProviderRetryResult)
    def retry_failed_providers(request: ProviderRetryRequest = _DEFAULT_PROVIDER_RETRY_REQUEST) -> ProviderRetryResult:
        now = datetime.now(UTC)
        queued = 0
        with session_factory() as session:
            jobs = JobRepository(session)
            for record in library_records(session):
                for source in record.sources:
                    if (
                        source.disappeared_at is not None
                        or source.intake_state == 'replaced'
                        or (not request.retry_all and not _needs_analysis_retry(source))
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

    @router.post(
        '/api/library/records/{record_id}/sources/{source_id}/destination-conflict/cleanup',
        response_model=DestinationConflictCleanupResponse,
    )
    def cleanup_destination_conflict(record_id: str, source_id: str) -> DestinationConflictCleanupResponse:
        try:
            with session_factory() as session:
                output_root = writable_media_root(session)
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                conflict = destination_conflict(session, record, source, output_root)
                if conflict is None:
                    raise HTTPException(status_code=404, detail='destination conflict not found')
                if conflict['ownership'] == 'managed':
                    raise HTTPException(status_code=409, detail='managed destination must be replaced by the worker')
                path = Path(conflict['path'])
                _remove_conflict_path(path)
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
                    removed=not path.exists(),
                    queued=queued,
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @router.post(
        '/api/library/records/{record_id}/sources/{source_id}/destination-conflict/replace',
        response_model=DestinationConflictCleanupResponse,
    )
    def replace_destination_conflict(record_id: str, source_id: str) -> DestinationConflictCleanupResponse:
        try:
            with session_factory() as session:
                output_root = writable_media_root(session)
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                conflict = destination_conflict(session, record, source, output_root)
                if conflict is None:
                    raise HTTPException(status_code=404, detail='destination conflict not found')
                if conflict['ownership'] != 'managed':
                    raise HTTPException(status_code=409, detail='only service-owned destinations can be replaced')
                path = Path(conflict['path'])
                if output_root is None or path == output_root.resolve() or output_root.resolve() not in path.parents:
                    raise HTTPException(status_code=409, detail='destination is outside the media root')
                _remove_conflict_path(path)
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
                    removed=not path.exists(),
                    queued=kind is not None,
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    @router.post('/api/library/records/{record_id}/sources/{source_id}/provider-retry')
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
                if source.disappeared_at is not None or source.intake_state == 'replaced':
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

    return router
