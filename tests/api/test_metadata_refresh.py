from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base, JobRecord, LibraryRecord, SourceRecord, SourceRootRecord


def _source(root: SourceRootRecord, record: LibraryRecord, path: Path, source_id: str) -> SourceRecord:
    return SourceRecord(
        id=source_id,
        source_path=str(path),
        device=1,
        inode=1,
        size_bytes=path.stat().st_size,
        sha256='a' * 64,
        duration_seconds=180,
        origin='manual',
        intake_state='present',
        source_root=root,
        library_record=record,
    )


def test_metadata_refresh_queues_musicbrainz_for_known_identities_once(tmp_path: Path) -> None:
    # Given: two active sources have known MusicBrainz IDs and another does not.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "metadata-refresh.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 23, tzinfo=UTC)
    known_path = tmp_path / 'known.flac'
    another_known_path = tmp_path / 'another-known.flac'
    unknown_path = tmp_path / 'unknown.flac'
    _ = known_path.write_bytes(b'known')
    _ = another_known_path.write_bytes(b'another-known')
    _ = unknown_path.write_bytes(b'unknown')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path=str(tmp_path),
            created_at=now,
            updated_at=now,
        )
        known = LibraryRecord(
            id='known-record',
            musicbrainz_recording_id='aaaf4974-c2bd-41dc-9d10-b33f080957cd',
            musicbrainz_release_id='fc71b952-9e68-4b22-bfdc-053233c2adbc',
            created_at=now,
            updated_at=now,
        )
        another_known = LibraryRecord(
            id='another-known-record',
            musicbrainz_recording_id='9eedf98a-94e5-43a6-8b32-50e3a3f850a3',
            created_at=now,
            updated_at=now,
        )
        unknown = LibraryRecord(id='unknown-record', created_at=now, updated_at=now)
        session.add_all(
            (
                root,
                known,
                another_known,
                unknown,
                _source(root, known, known_path, 'known-source'),
                _source(root, another_known, another_known_path, 'another-known-source'),
                _source(root, unknown, unknown_path, 'unknown-source'),
            )
        )
        session.commit()

    client = TestClient(create_app(lambda: Session(engine)))
    statements: list[str] = []

    def capture_statement(
        _connection: object,
        _cursor: object,
        statement: str,
        _parameters: object,
        _context: object,
        _executemany: bool,
    ) -> None:
        statements.append(statement)

    event.listen(
        engine,
        'before_cursor_execute',
        capture_statement,
    )

    # When: the operator requests the bulk refresh twice before the worker runs.
    first = client.post('/api/library/metadata/refresh')
    second = client.post('/api/library/metadata/refresh')

    # Then: only known identities each have one coalesced MusicBrainz analysis job.
    assert first.status_code == 200
    assert first.json() == {'queued': 2}
    assert second.status_code == 200
    assert second.json() == {'queued': 0}
    assert not any('candidate_evidence' in statement for statement in statements)
    job_queries = [statement for statement in statements if 'FROM jobs' in statement]
    assert len(job_queries) == 2
    with Session(engine) as session:
        jobs = tuple(session.scalars(select(JobRecord).where(JobRecord.kind == 'musicbrainz_analysis')))
        assert {job.source_id for job in jobs} == {'known-source', 'another-known-source'}
