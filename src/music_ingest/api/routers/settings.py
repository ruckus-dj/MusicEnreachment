from __future__ import annotations

from collections.abc import Callable
from pathlib import Path

from fastapi import APIRouter, HTTPException, Response, status
from sqlalchemy.orm import Session

from music_ingest.api.dependencies import SessionFactory
from music_ingest.contracts import (
    CurrentStateCleanupResponse,
    MatchingSettings,
    NextUnsortedFilenameResponse,
    RuntimeSettingsRequest,
    RuntimeSettingsResponse,
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
from music_ingest.models import SourceRootRecord
from music_ingest.services.metadata import UnsortedFilenameSuffixError, allocate_unsorted_filename
from music_ingest.services.reconciliation import CurrentStateCleanupReport, current_state_cleanup
from music_ingest.services.settings import RuntimeSettings, build_runtime_settings, save_runtime_settings
from music_ingest.services.source_roots import SourceRootConflictError, SourceRootService, SourceRootValidationError
from music_ingest.services.storage import StorageService, StorageValidationError


def _settings_response(settings: RuntimeSettings) -> RuntimeSettingsResponse:
    return RuntimeSettingsResponse(
        confidence_threshold=settings.confidence_threshold,
        timeout_seconds=settings.timeout_seconds,
        retry_delay_seconds=settings.retry_delay_seconds,
        max_attempts=settings.max_attempts,
        worker_pools=settings.worker_pools,
        musicbrainz_enabled=settings.musicbrainz_enabled,
        musicbrainz_user_agent=settings.musicbrainz_user_agent,
        musicbrainz_host=settings.musicbrainz_host,
        musicbrainz_request_delay_seconds=settings.musicbrainz_request_delay_seconds,
        acoustid_enabled=settings.acoustid_enabled,
        acoustid_request_delay_seconds=settings.acoustid_request_delay_seconds,
        acoustid_client_key_configured=bool(settings.acoustid_client_key),
        artwork_enabled=settings.artwork_enabled,
        lrclib_enabled=settings.lrclib_enabled,
        lrclib_host=settings.lrclib_host,
        lrclib_user_agent=settings.lrclib_user_agent,
        lrclib_timeout_seconds=settings.lrclib_timeout_seconds,
        lrclib_max_attempts=settings.lrclib_max_attempts,
        lrclib_request_delay_seconds=settings.lrclib_request_delay_seconds,
        lrclib_max_response_bytes=settings.lrclib_max_response_bytes,
        lrclib_match_confidence_threshold=settings.lrclib_match_confidence_threshold,
    )


def _cleanup_response(report: CurrentStateCleanupReport) -> CurrentStateCleanupResponse:
    return CurrentStateCleanupResponse(
        source_count=len(report.source_ids),
        library_record_count=len(report.library_record_ids),
        applied=report.applied,
    )


def create_router(
    session_factory: SessionFactory,
    *,
    source_roots_parent: Path | None = None,
    media_root: Path | None = None,
    storage_browse_roots: tuple[Path, ...] | None = None,
    on_runtime_settings_updated: Callable[[RuntimeSettings], None] | None = None,
) -> APIRouter:
    router = APIRouter()

    def source_root_service(session: Session) -> SourceRootService:
        return SourceRootService(session, source_roots_parent if media_root is None else None)

    def storage_service(session: Session) -> StorageService:
        if media_root is None:
            raise HTTPException(status_code=503, detail='storage administration is not configured')
        browse_roots = storage_browse_roots or tuple(
            root for root in (source_roots_parent, media_root.parent) if root is not None
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

    @router.get('/api/settings', response_model=RuntimeSettingsResponse)
    def runtime_settings() -> RuntimeSettingsResponse:
        with session_factory() as session:
            return _settings_response(build_runtime_settings(session))

    @router.post(
        '/api/settings/maintenance/current-state/preview',
        response_model=CurrentStateCleanupResponse,
    )
    def preview_current_state_cleanup() -> CurrentStateCleanupResponse:
        with session_factory() as session:
            return _cleanup_response(current_state_cleanup(session))

    @router.post(
        '/api/settings/maintenance/current-state/apply',
        response_model=CurrentStateCleanupResponse,
    )
    def apply_current_state_cleanup() -> CurrentStateCleanupResponse:
        with session_factory() as session:
            report = current_state_cleanup(session, apply=True)
            session.commit()
            return _cleanup_response(report)

    @router.get('/api/settings/source-roots', response_model=SourceRootListResponse)
    def list_source_roots() -> SourceRootListResponse:
        try:
            with session_factory() as session:
                service = source_root_service(session)
                return SourceRootListResponse(items=tuple(source_root_response(root) for root in service.list()))
        except SourceRootValidationError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @router.get('/api/settings/source-roots/candidates', response_model=SourceRootCandidateListResponse)
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

    @router.get('/api/settings/storage/browser', response_model=StorageBrowserResponse)
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

    @router.get('/api/settings/storage', response_model=StorageConfigResponse)
    def storage_config() -> StorageConfigResponse:
        with session_factory() as session:
            config = storage_service(session).config()
            session.commit()
            return StorageConfigResponse(
                output_root=config.output_root,
                state=config.state,
                generation=config.generation,
            )

    @router.get('/api/settings/storage/next-unsorted-filename', response_model=NextUnsortedFilenameResponse)
    def next_unsorted_filename(suffix: str = '.mka') -> NextUnsortedFilenameResponse:
        try:
            with session_factory() as session:
                filename = allocate_unsorted_filename(session, suffix)
                session.commit()
                return NextUnsortedFilenameResponse(filename=filename)
        except UnsortedFilenameSuffixError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error

    @router.post('/api/settings/storage/output/preview', response_model=StorageOutputPreviewResponse)
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

    @router.put('/api/settings/storage/output', response_model=StorageConfigResponse)
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

    @router.post('/api/settings/source-roots', response_model=SourceRootResponse, status_code=status.HTTP_201_CREATED)
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

    @router.delete('/api/settings/source-roots/{root_id}', status_code=status.HTTP_204_NO_CONTENT)
    def delete_source_root(root_id: str) -> Response:
        with session_factory() as session:
            if not source_root_service(session).remove(root_id):
                raise HTTPException(status_code=404, detail='source root not found')
            session.commit()
            return Response(status_code=status.HTTP_204_NO_CONTENT)

    @router.put('/api/settings/source-roots/{root_id}', response_model=SourceRootResponse)
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

    @router.put('/api/settings', response_model=RuntimeSettingsResponse)
    def update_runtime_settings(request: RuntimeSettingsRequest) -> RuntimeSettingsResponse:
        with session_factory() as session:
            current = build_runtime_settings(session)
            settings = RuntimeSettings(
                confidence_threshold=request.confidence_threshold,
                timeout_seconds=request.timeout_seconds,
                retry_delay_seconds=request.retry_delay_seconds,
                max_attempts=request.max_attempts,
                worker_pools=request.worker_pools,
                musicbrainz_enabled=request.musicbrainz_enabled,
                musicbrainz_user_agent=request.musicbrainz_user_agent,
                musicbrainz_host=request.musicbrainz_host,
                musicbrainz_request_delay_seconds=request.musicbrainz_request_delay_seconds,
                acoustid_enabled=request.acoustid_enabled,
                acoustid_request_delay_seconds=request.acoustid_request_delay_seconds,
                acoustid_client_key=(
                    current.acoustid_client_key if request.acoustid_client_key is None else request.acoustid_client_key
                ),
                artwork_enabled=request.artwork_enabled,
                lrclib_enabled=request.lrclib_enabled,
                lrclib_host=request.lrclib_host,
                lrclib_user_agent=request.lrclib_user_agent,
                lrclib_timeout_seconds=request.lrclib_timeout_seconds,
                lrclib_max_attempts=request.lrclib_max_attempts,
                lrclib_request_delay_seconds=request.lrclib_request_delay_seconds,
                lrclib_max_response_bytes=request.lrclib_max_response_bytes,
                lrclib_match_confidence_threshold=request.lrclib_match_confidence_threshold,
            )
            if settings.acoustid_enabled and not settings.acoustid_client_key:
                raise HTTPException(status_code=422, detail='AcoustID requires a client key when enabled')
            save_runtime_settings(session, settings)
            session.commit()
            if on_runtime_settings_updated is not None:
                on_runtime_settings_updated(settings)
            return _settings_response(settings)

    @router.get('/api/settings/matching', response_model=MatchingSettings)
    def matching_settings() -> MatchingSettings:
        with session_factory() as session:
            return MatchingSettings(confidence_threshold=build_runtime_settings(session).confidence_threshold)

    @router.put('/api/settings/matching', response_model=MatchingSettings)
    def update_matching_settings(request: MatchingSettings) -> MatchingSettings:
        with session_factory() as session:
            current = build_runtime_settings(session)
            save_runtime_settings(
                session,
                current.model_copy(update={'confidence_threshold': request.confidence_threshold}),
            )
            session.commit()
            return request

    return router
