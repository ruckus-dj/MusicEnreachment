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
    MusicBrainzReleaseLookup,
)
from music_ingest.models import (
    LibraryRecord,
    ReviewDecisionRecord,
    SourceRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.association import (
    ManualAssociationRequest,
    RecordingAssociationService,
    RecordingAssociationUnavailable,
)
from music_ingest.services.candidates import release_candidate_records
from music_ingest.services.library.service import (
    append_metadata_revision,
    library_record_detail,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.services.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceService
from music_ingest.services.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.services.matching.providers import (
    Ambiguous,
    FixtureCase,
    MusicBrainzMatch,
    MusicBrainzProvider,
    NoMatch,
)
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
                        content={
                            'candidate_key': candidate.candidate_key,
                            'record_id': record.id,
                            'revision': None,
                            'queued': bool(queued),
                        }
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
                        ManualAssociationRequest(source.id, candidate_recording_mbid, now, candidate_release_mbid)
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
                        'record_id': record.id,
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
    def override_musicbrainz_recording(record_id: str, source_id: str, request: MusicBrainzOverride) -> JSONResponse:
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
                    ManualAssociationRequest(source.id, request.recording_mbid.lower(), now)
                )
                session.commit()
                return JSONResponse(
                    content={'recording_mbid': request.recording_mbid.lower(), 'record_id': result.library_record_id}
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error
        except RecordingAssociationUnavailable as error:
            raise HTTPException(status_code=503, detail=str(error)) from error

    @router.post('/api/library/records/{record_id}/sources/{source_id}/musicbrainz/release-candidates')
    def load_musicbrainz_release_candidates(
        record_id: str, source_id: str, request: MusicBrainzReleaseLookup
    ) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                source = next((item for item in record.sources if item.id == source_id), None)
                if source is None:
                    raise LookupError(source_id)
                _ = require_owned_source(session, source.id)
                persisted_source = SourceRecord.get(session, source.id)
                if persisted_source is None:
                    raise LookupError(source.id)
                recording_mbid = None if request.recording_mbid is None else request.recording_mbid.lower()
                if recording_mbid is None:
                    recording_mbid = (
                        persisted_source.association_override.recording_mbid
                        if persisted_source.association_override is not None
                        else record.musicbrainz_recording_id
                    )
                if recording_mbid is None:
                    raise HTTPException(
                        status_code=409,
                        detail='select a recording MBID before loading a release',
                    )
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
                release_mbid = None if request.release_mbid is None else request.release_mbid.lower()
                source_tags = _catalog_tags(record, source.id)
                result = (
                    ProviderEvidenceService(session, provider, None)
                    .lookup(
                        ProviderEvidenceRequest(
                            query='',
                            musicbrainz_case=FixtureCase.SUCCESS,
                            fingerprint=None,
                            acoustid_case=None,
                            force_refresh=True,
                            release_title=source_tags.get('ALBUM'),
                            artist_name=source_tags.get('ARTIST'),
                            recording_mbid=recording_mbid,
                            release_mbid=release_mbid,
                            recording_title=source_tags.get('TITLE'),
                            duration_seconds=persisted_source.duration_seconds,
                            run_acoustid=False,
                            run_musicbrainz=True,
                        ),
                        datetime.now(UTC),
                    )
                    .musicbrainz
                )
                if isinstance(result, NoMatch):
                    detail = (
                        'recording has no available releases'
                        if release_mbid is None
                        else 'recording is not present on the requested release'
                    )
                    raise HTTPException(status_code=409, detail=detail)
                if isinstance(result, MusicBrainzMatch):
                    provider_candidates = (result.candidate,)
                elif isinstance(result, Ambiguous):
                    provider_candidates = result.candidates
                else:
                    raise HTTPException(status_code=503, detail='MusicBrainz release is unavailable')
                verified_candidates = tuple(
                    candidate
                    for candidate in provider_candidates
                    if recording_mbid in candidate.recording_mbids
                    and (release_mbid is None or candidate.release_mbid == release_mbid)
                )
                if not verified_candidates:
                    raise HTTPException(status_code=409, detail='recording is not present on the requested release')
                candidates = tuple(
                    persisted
                    for candidate in verified_candidates
                    for persisted in release_candidate_records(source.id, candidate)
                )
                session.add_all(candidates)
                session.commit()
                return JSONResponse(
                    content={
                        'recording_mbid': recording_mbid,
                        'release_mbid': release_mbid,
                        'status': 'review_required',
                        'candidate_count': len(candidates),
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record or source not found') from error

    return router
