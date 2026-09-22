from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException, Response, status
from fastapi.responses import JSONResponse
from sqlalchemy.exc import OperationalError

from music_ingest.api.dependencies import SessionFactory
from music_ingest.api.library_access import require_owned_source
from music_ingest.contracts import (
    LibraryIdentityUpdate,
    MetadataUpdate,
)
from music_ingest.contracts.source_encoding import EncodingApplied, EncodingDetail, EncodingPreview, EncodingRequest
from music_ingest.models import SourceRecord
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import (
    append_metadata_revision,
    attach_source,
    library_record_detail,
    record_event,
)
from music_ingest.services.source_encoding import (
    EncodingConflict,
    EncodingInvalid,
    apply_encoding,
    encoding_detail,
    preview_encoding,
)


def create_router(session_factory: SessionFactory) -> APIRouter:
    router = APIRouter()

    @router.post('/api/library/records/{record_id}/sources/{source_id}', status_code=status.HTTP_204_NO_CONTENT)
    def attach_library_source(record_id: str, source_id: str) -> Response:
        try:
            with session_factory() as session:
                attach_source(session, source_id, record_id)
                session.commit()
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error
        return Response(status_code=status.HTTP_204_NO_CONTENT)

    @router.put('/api/library/records/{record_id}/identity')
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

    @router.put('/api/library/records/{record_id}/metadata')
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

    @router.get('/api/sources/{source_id}/encoding', response_model=EncodingDetail)
    def source_encoding_detail(source_id: str) -> EncodingDetail:
        with session_factory() as session:
            source = session.get(SourceRecord, source_id)
            if source is None:
                raise HTTPException(status_code=404, detail='source not found')
            return encoding_detail(source)

    @router.post('/api/sources/{source_id}/encoding/preview', response_model=EncodingPreview)
    def source_encoding_preview(source_id: str, request: EncodingRequest) -> EncodingPreview:
        with session_factory() as session:
            source = session.get(SourceRecord, source_id)
            if source is None:
                raise HTTPException(status_code=404, detail='source not found')
            try:
                return preview_encoding(source, request)
            except EncodingConflict as error:
                raise HTTPException(status_code=409, detail=str(error)) from error

    @router.post('/api/sources/{source_id}/encoding/apply', response_model=EncodingApplied)
    def source_encoding_apply(source_id: str, request: EncodingRequest) -> EncodingApplied:
        with session_factory() as session:
            source = session.get(SourceRecord, source_id)
            if source is None:
                raise HTTPException(status_code=404, detail='source not found')
            try:
                result = apply_encoding(session, source, request, datetime.now(UTC))
                session.commit()
                return result
            except EncodingConflict as error:
                raise HTTPException(status_code=409, detail=str(error)) from error
            except EncodingInvalid as error:
                raise HTTPException(status_code=422, detail=error.preview.model_dump()) from error
            except OperationalError as error:
                raise HTTPException(status_code=409, detail='source_processing_busy') from error

    return router
