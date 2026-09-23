from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select, text
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from music_ingest.contracts.source_encoding import EncodingChoice, EncodingRequest
from music_ingest.models import Base, SourceRecord, SourceRootRecord, SourceTagRecord
from music_ingest.services.library.service import ensure_source_record
from music_ingest.services.publication import PublicationAttemptRequest, reserve_attempt
from music_ingest.services.source_encoding import EncodingConflict, apply_encoding


@pytest.mark.postgres
def test_encoding_apply_conflicts_with_uncommitted_publication_reservation(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    now = datetime.now(UTC)
    with PostgresContainer('postgres:17') as postgres:
        engine = create_engine(postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg'))
        Base.metadata.create_all(engine)
        with Session(engine) as setup:
            setup.add(
                SourceRootRecord(
                    id='legacy',
                    display_name='legacy',
                    canonical_path='/sources/legacy',
                    enabled=True,
                    scan_state='scanned',
                    created_at=now,
                    updated_at=now,
                )
            )
            setup.flush()
            source = SourceRecord(
                id='source',
                source_path='/absent',
                device=1,
                inode=1,
                size_bytes=1,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
            )
            source.tag_observations.append(SourceTagRecord(format_name='mp3', tag_name='TITLE', value='ASCII'))
            setup.add(source)
            setup.flush()
            record = ensure_source_record(setup, source, now)
            record_id, field_id = record.id, source.tag_observations[0].id
            setup.commit()
        with Session(engine) as holder, Session(engine) as contender:
            assert (
                reserve_attempt(
                    holder,
                    PublicationAttemptRequest(
                        'attempt',
                        record_id,
                        'source',
                        None,
                        Path('/managed'),
                        'audio.mka',
                        Path('/stage'),
                        Path('/backup'),
                        now,
                    ),
                )
                is not None
            )
            contender.execute(text("SET statement_timeout = '1000ms'"))
            source = contender.get(SourceRecord, 'source')
            assert source is not None
            request = EncodingRequest(expected_revision=1, choices=[EncodingChoice(field_id=field_id, mode='original')])
            with pytest.raises(EncodingConflict, match='source_processing_busy'):
                apply_encoding(contender, source, request, now)
            # Failed savepoint must release the source lock while contender stays alive.
            with Session(engine) as probe:
                assert probe.scalar(
                    select(SourceRecord).where(SourceRecord.id == 'source').with_for_update(nowait=True, key_share=True)
                )
            holder.commit()
            # The committed journal now rejects apply even though the reservation lock is free.
            with pytest.raises(EncodingConflict, match='source_processing_busy'):
                apply_encoding(contender, source, request, now)
            assert source.source_metadata_revision == 1
        engine.dispose()
