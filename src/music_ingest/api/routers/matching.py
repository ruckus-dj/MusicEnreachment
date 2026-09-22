from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException
from fastapi.responses import JSONResponse
from sqlalchemy import select

from music_ingest.adapters.external.musicbrainz import SyncMusicBrainzTransport
from music_ingest.api.catalog_views import _catalog_tags
from music_ingest.api.dependencies import SessionFactory
from music_ingest.api.library_access import require_owned_source
from music_ingest.contracts import (
    CandidateEvidencePayload,
    CandidateSelection,
    ManualSourceSelection,
    MusicBrainzOverride,
)
from music_ingest.models import (
    LibraryRecord,
    ReviewDecisionRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.association import (
    ManualAssociationRequest,
    RecordingAssociationService,
    RecordingAssociationUnavailable,
)
from music_ingest.services.library.service import (
    append_metadata_revision,
    library_record_detail,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.services.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.services.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.services.matching.providers import Ambiguous, FixtureCase, MusicBrainzMatch, MusicBrainzProvider
from music_ingest.services.settings import (
    build_runtime_settings,
)


def create_router(
    session_factory: SessionFactory,
    *,
    musicbrainz_provider: MusicBrainzProvider | None = None,
    musicbrainz_transport: SyncMusicBrainzTransport | None = None,
) -> APIRouter:
    router = APIRouter()

    @router.get('/api/library/records/{record_id}/sources/{source_id}/candidates/{candidate_key}/musicbrainz')
    def decode_acoustid_candidate(record_id: str, source_id: str, candidate_key: str) -> JSONResponse:
        try:
            with session_factory() as session:
                provider = musicbrainz_provider
                if provider is None and musicbrainz_transport is not None:
                    settings = build_runtime_settings(session)
                    provider = MusicBrainzProviderAdapter(
                        musicbrainz_transport,
                        settings.musicbrainz_user_agent,
                        settings.musicbrainz_host,
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

    @router.post('/api/library/records/{record_id}/sources/{source_id}/candidates/select')
    def select_provider_candidate(record_id: str, source_id: str, request: CandidateSelection) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                candidate = next(
                    (
                        item
                        for item in reversed(source.candidates)
                        if item.candidate_key == request.candidate_key
                        and CandidateEvidencePayload.model_validate_json(item.evidence).entity == request.entity
                    ),
                    None,
                )
                if candidate is None:
                    raise HTTPException(status_code=404, detail='provider candidate not found')
                evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
                if evidence.provider != request.provider:
                    raise HTTPException(status_code=409, detail='candidate belongs to another provider')
                now = datetime.now(UTC)
                if request.entity == 'recording' or request.provider == 'acoustid':
                    record.musicbrainz_recording_id = candidate.candidate_key
                    compatible_ids = evidence.compatible_ids
                    if (
                        record.musicbrainz_release_id is not None
                        and compatible_ids
                        and record.musicbrainz_release_id not in compatible_ids
                    ):
                        record.musicbrainz_release_id = None
                    queued = JobRepository(session).requeue_provider(source.id, 'musicbrainz', now)
                    session.add(
                        ReviewDecisionRecord(
                            source_id=source.id,
                            state='acoustid_confirmed' if request.provider == 'acoustid' else 'recording_confirmed',
                            rationale=f'{request.provider} recording {candidate.candidate_key} selected by reviewer',
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
                        content={'candidate_key': candidate.candidate_key, 'revision': None, 'queued': bool(queued)}
                    )
                candidate_tags = evidence.tags
                if not candidate_tags:
                    raise HTTPException(status_code=409, detail='provider candidate has no metadata')
                candidate_recording_mbid = evidence.recording_mbid
                candidate_release_mbid = evidence.release_mbid
                if candidate_release_mbid is None:
                    raise HTTPException(status_code=409, detail='provider candidate has no release identity')
                if candidate_recording_mbid is None:
                    raise HTTPException(status_code=409, detail='provider candidate has no recording identity')
                target_record = session.scalar(
                    select(LibraryRecord)
                    .where(LibraryRecord.musicbrainz_recording_id == candidate_recording_mbid)
                    .where(LibraryRecord.musicbrainz_release_id == candidate_release_mbid)
                )
                if target_record is not None and target_record.id != record.id:
                    association = RecordingAssociationService(session).associate_verified_manual(
                        ManualAssociationRequest(source.id, candidate_recording_mbid, now)
                    )
                    session.expire_all()
                    record = library_record_detail(session, association.library_record_id)
                source_tags = _catalog_tags(record, source.id)
                final_tags = {**source_tags, **candidate_tags}
                analyzed = append_metadata_revision(
                    session, record.id, source.id, 'analyzed', candidate_tags, 'review', now
                )
                final = append_metadata_revision(session, record.id, source.id, 'final', final_tags, 'review', now)
                record.match_state = 'matched'
                record.musicbrainz_release_id = candidate_release_mbid
                record.musicbrainz_recording_id = candidate_recording_mbid
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

    @router.post('/api/library/records/{record_id}/effective-source')
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

    @router.post('/api/library/records/{record_id}/sources/{source_id}/musicbrainz/override')
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
                    settings = build_runtime_settings(session)
                    provider = MusicBrainzProviderAdapter(
                        musicbrainz_transport,
                        settings.musicbrainz_user_agent,
                        settings.musicbrainz_host,
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

    return router
