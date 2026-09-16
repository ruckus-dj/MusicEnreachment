from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobAttemptRecord, JobRecord, SourceRecord
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing import ProcessingConfig, ProcessingWorker


def test_stale_source_revision_job_is_superseded_before_handler(tmp_path: Path) -> None:
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        source = SourceRecord(
            id='source',
            source_path='/absent',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
            source_metadata_revision=2,
        )
        session.add(source)
        session.flush()
        job = JobRecord(
            id='job',
            source_id=source.id,
            source_metadata_revision=1,
            kind='musicbrainz_analysis',
            state='running',
            created_at=now,
        )
        attempt = JobAttemptRecord(state='running')
        worker = ProcessingWorker(session, ProcessingConfig(tmp_path, tmp_path / 'stage', tmp_path / 'out'))
        worker._process(ClaimedJob(job, attempt), now)
        assert job.state == 'superseded'
        assert attempt.state == 'succeeded'


def test_automatic_final_preserves_manual_revision() -> None:
    from music_ingest.models import LibraryMetadataRevisionRecord, LibraryRecord
    from music_ingest.processing.handlers.selection import refreshed_final_tags

    record = LibraryRecord(id='record')
    record.metadata_revisions.append(
        LibraryMetadataRevisionRecord(source_id='source', layer='final', actor='manual', tags_json='{"TITLE":"Manual"}')
    )
    assert refreshed_final_tags(record, 'source', {'TITLE': 'Source'}, {'TITLE': 'Provider'}) == {'TITLE': 'Manual'}
    record.metadata_revisions.clear()
    assert refreshed_final_tags(record, 'source', {'TITLE': 'Source', 'GENRE': 'Rock'}, {'TITLE': 'Provider'}) == {
        'TITLE': 'Provider',
        'GENRE': 'Rock',
    }
