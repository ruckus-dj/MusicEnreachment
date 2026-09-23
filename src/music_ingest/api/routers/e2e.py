from __future__ import annotations

import json
from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse
from sqlalchemy import select

from music_ingest.api.dependencies import SessionFactory
from music_ingest.models import (
    CandidateRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceRecord,
    SourceRootRecord,
    SourceTagRecord,
)

_E2E_RECORD_ID = 'e2e-record'
_E2E_SOURCE_IDS = ('e2e-source-a', 'e2e-source-b')
_E2E_RECORDING_MBID = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
_E2E_RELEASE_MBID = '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'
_E2E_CORRECTION_MBID = '11111111-1111-4111-8111-111111111111'


def create_router(session_factory: SessionFactory) -> APIRouter:
    router = APIRouter()

    @router.post('/api/e2e/seed')
    def seed_e2e_fixtures(request: Request) -> JSONResponse:
        if not request.app.state.e2e_seed_enabled:
            raise HTTPException(status_code=404, detail='not found')
        now = datetime.now(UTC)
        with session_factory() as session:
            root = session.scalar(select(SourceRootRecord).where(SourceRootRecord.id == 'e2e'))
            if root is None:
                root = SourceRootRecord(
                    id='e2e',
                    display_name='E2E fixture source',
                    canonical_path='e2e://',
                    enabled=True,
                    scan_state='never_scanned',
                    created_at=now,
                    updated_at=now,
                )
                session.add(root)
            record = session.get(LibraryRecord, _E2E_RECORD_ID)
            if record is None:
                record = LibraryRecord(
                    id=_E2E_RECORD_ID,
                    musicbrainz_recording_id=_E2E_RECORDING_MBID,
                    musicbrainz_release_id=_E2E_RELEASE_MBID,
                    created_at=now,
                    updated_at=now,
                    match_state='matched',
                )
                session.add(record)
            for index, source_id in enumerate(_E2E_SOURCE_IDS):
                source = session.get(SourceRecord, source_id)
                if source is None:
                    source = SourceRecord(
                        id=source_id,
                        source_path=f'e2e://{source_id}.flac',
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
                                            'MUSICBRAINZ_ALBUMID': _E2E_RELEASE_MBID,
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
            record.musicbrainz_release_id = _E2E_RELEASE_MBID
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

    return router
