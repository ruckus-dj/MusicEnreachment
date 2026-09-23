from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    CandidateRecord,
    FingerprintRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceRecord,
)
from music_ingest.services.association import AutomaticAssociationRequest, RecordingAssociationService


def test_automatic_association_when_identical_content_has_other_recording_requires_review(tmp_path: Path) -> None:
    # Given: two immutable observations of identical audio already grouped under recording A.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "content-conflict.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 14, tzinfo=UTC)
    recording_a = '47d13484-9eed-4460-babd-bca3a19fcd77'
    recording_b = '48c984ee-2333-442b-9483-f091162f2a62'
    with Session(engine) as session:
        record = LibraryRecord(
            id='record-vol-1',
            musicbrainz_recording_id=recording_a,
            musicbrainz_release_id='release-a',
            created_at=now,
            updated_at=now,
        )
        source_one = SourceRecord(
            id='source-downloads',
            source_path='/downloads/01 - Песня для радио.flac',
            device=37,
            inode=416,
            size_bytes=20570541,
            sha256='d' * 64,
            duration_seconds=None,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        source_two = SourceRecord(
            id='source-incoming',
            source_path='/incoming/01 - Песня для радио.flac',
            device=37,
            inode=416,
            size_bytes=20570541,
            sha256='d' * 64,
            duration_seconds=None,
            origin='manual',
            intake_state='present',
            library_record=record,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz',
                    outcome='musicbrainzmatch',
                    snapshot_sha256='a' * 64,
                    snapshot='{}',
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='release-vol-2',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.96927744,"tags":'
                        '{"MUSICBRAINZ_TRACKID":"48c984ee-2333-442b-9483-f091162f2a62"}}'
                    ),
                )
            ],
        )
        session.add_all(
            (
                record,
                source_one,
                source_two,
                FingerprintRecord(
                    source=source_one,
                    state='success',
                    fingerprint='identical-acoustic-fingerprint',
                    duration_seconds=241,
                    tool_version='fixture',
                    output_sha256='e' * 64,
                ),
                FingerprintRecord(
                    source=source_two,
                    state='success',
                    fingerprint='identical-acoustic-fingerprint',
                    duration_seconds=241,
                    tool_version='fixture',
                    output_sha256='f' * 64,
                ),
            )
        )
        session.commit()

        # When: automatic evidence proposes recording B for only one identical-content observation.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(source_two.id, recording_b, 0.96927744, 0.7, '{}', now)
        )
        session.commit()

        # Then: the source remains grouped with recording A and is explicitly reviewable.
        persisted = session.get(SourceRecord, source_two.id)
        assert result is None
        assert persisted is not None
        assert persisted.library_record_id == record.id
        assert persisted.review_decisions[-1].state == 'association_review_required'
