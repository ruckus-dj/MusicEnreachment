from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.bootstrap.server import ensure_unsorted_filename_counter
from music_ingest.models import Base, UnsortedFilenameCounterRecord
from music_ingest.services.metadata import allocate_unsorted_filename, allocate_unsorted_filename_with_factory


def test_allocate_unsorted_filename_increments_the_persisted_counter(tmp_path: Path) -> None:
    # Given: a database containing the durable Unsorted filename counter.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "counter.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        ensure_unsorted_filename_counter(session)
        # When: two independent allocations request the canonical publication extension.
        first = allocate_unsorted_filename(session, '.mka')
        session.commit()
    with Session(engine) as session:
        second = allocate_unsorted_filename(session, '.mka')
        session.commit()

    # Then: each allocation receives the next database-backed number.
    assert first == 'Track 01.mka'
    assert second == 'Track 02.mka'


def test_next_unsorted_filename_endpoint_commits_each_allocation(tmp_path: Path) -> None:
    # Given: an API backed by a database counter.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "endpoint.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        ensure_unsorted_filename_counter(session)
        session.commit()
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the endpoint is requested twice.
    first = client.get('/api/settings/storage/next-unsorted-filename', params={'suffix': '.mka'})
    second = client.get('/api/settings/storage/next-unsorted-filename', params={'suffix': '.mka'})

    # Then: committed allocations are sequential and preserve supported suffixes.
    assert first.json() == {'filename': 'Track 01.mka'}
    assert second.json() == {'filename': 'Track 02.mka'}


def test_startup_initializes_missing_unsorted_filename_counter(tmp_path: Path) -> None:
    # Given: a migrated database without the application-owned counter row.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "startup.db"}')
    Base.metadata.create_all(engine)

    # When: startup initializes durable application state.
    with Session(engine) as session:
        ensure_unsorted_filename_counter(session)
        session.commit()

    # Then: the counter exists at its initial value before any endpoint request.
    with Session(engine) as session:
        counter = session.get(UnsortedFilenameCounterRecord, 1)
        assert counter is not None
        assert counter.next_number == 0


def test_factory_allocator_uses_a_separate_committed_session(tmp_path: Path) -> None:
    # Given: a session factory and a migrated counter table.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "factory.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        ensure_unsorted_filename_counter(session)
        session.commit()

    def factory() -> Session:
        return Session(engine)

    # When: allocation runs through the factory.
    filename = allocate_unsorted_filename_with_factory(factory, '.mka')

    # Then: the caller receives the committed next filename.
    with Session(engine) as session:
        assert filename == 'Track 01.mka'
        assert allocate_unsorted_filename(session, '.mka') == 'Track 02.mka'
