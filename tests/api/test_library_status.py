from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import (
    Base,
    LibraryPublicationRecord,
    LibraryRecord,
    LibraryRecordConsolidationRecord,
    SourceRecord,
)


def test_library_status_counts_active_non_retired_records(tmp_path: Path) -> None:
    # Given: active source/publication records, an inactive orphan, and a retired alias.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "library-status.db"}')
    Base.metadata.create_all(engine)
    observed_at = datetime(2026, 9, 24, tzinfo=UTC)
    with Session(engine) as session:
        session.add_all(
            (
                LibraryRecord(
                    id='record-ready', processing_state='complete', created_at=observed_at, updated_at=observed_at
                ),
                LibraryRecord(
                    id='record-processing',
                    processing_state='analyzing',
                    created_at=observed_at,
                    updated_at=observed_at,
                ),
                LibraryRecord(
                    id='record-published',
                    processing_state='complete',
                    publication_state='current',
                    created_at=observed_at,
                    updated_at=observed_at,
                ),
                LibraryRecord(
                    id='record-orphan',
                    processing_state='analyzing',
                    created_at=observed_at,
                    updated_at=observed_at,
                ),
                LibraryRecord(
                    id='record-retired',
                    processing_state='analyzing',
                    created_at=observed_at,
                    updated_at=observed_at,
                ),
                LibraryRecordConsolidationRecord(
                    retired_library_record_id='record-retired',
                    canonical_library_record_id='record-ready',
                    sha256='a' * 64,
                    created_at=observed_at,
                ),
                SourceRecord(
                    id='source-ready',
                    source_path='/incoming/ready.flac',
                    device=1,
                    inode=1,
                    size_bytes=3,
                    sha256='b' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    library_record_id='record-ready',
                ),
                SourceRecord(
                    id='source-processing',
                    source_path='/incoming/processing.flac',
                    device=1,
                    inode=2,
                    size_bytes=3,
                    sha256='c' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    library_record_id='record-processing',
                ),
                SourceRecord(
                    id='source-retired',
                    source_path='/incoming/retired.flac',
                    device=1,
                    inode=3,
                    size_bytes=3,
                    sha256='d' * 64,
                    duration_seconds=180,
                    origin='manual',
                    intake_state='present',
                    library_record_id='record-retired',
                ),
                LibraryPublicationRecord(
                    id='publication-current',
                    library_record_id='record-published',
                    source_id='source-ready',
                    path='/media/published.mka',
                    format_name='mka',
                    content_sha256='e' * 64,
                    metadata_revision_id=None,
                    state='current',
                    created_at=observed_at,
                ),
            )
        )
        session.commit()

    try:
        # When: the compact library status is requested.
        response = TestClient(create_app(lambda: Session(engine))).get('/api/library/status')

        # Then: only active non-retired records contribute and pending work is reported.
        assert response.status_code == 200
        assert response.json() == {'total_track_count': 3, 'has_analysis': True}
    finally:
        engine.dispose()


def test_library_record_collection_is_not_public(tmp_path: Path) -> None:
    # Given: an application with the replacement status endpoint.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "removed-record-list.db"}')
    Base.metadata.create_all(engine)

    try:
        # When: a client requests the removed record collection.
        response = TestClient(create_app(lambda: Session(engine))).get('/api/library/records')

        # Then: only source-specific record detail routes remain public.
        assert response.status_code == 404
    finally:
        engine.dispose()
