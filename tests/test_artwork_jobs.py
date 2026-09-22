from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord
from music_ingest.repositories.jobs import JobRepository


def test_enqueue_release_artwork_when_same_release_is_active_coalesces_job(tmp_path: Path) -> None:
    # Given: one database with an active artwork job for a release.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "artwork-jobs.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 18, tzinfo=UTC)
    with Session(engine) as session:
        repository = JobRepository(session)
        first = repository.enqueue_release_artwork('release-1', now)
        second = repository.enqueue_release_artwork('release-1', now)

        # When: the session is committed and the queued jobs are inspected.
        session.commit()

        # Then: only one independent release-targeted job exists.
        assert first is not None
        assert second is None
        jobs = session.scalars(select(JobRecord)).all()
        assert [(job.kind, job.release_mbid, job.source_id, job.library_record_id) for job in jobs] == [
            ('artwork_enrichment', 'release-1', None, None)
        ]
