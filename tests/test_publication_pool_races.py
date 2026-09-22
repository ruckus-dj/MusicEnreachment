from __future__ import annotations

from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path
from threading import Barrier, Event

import pytest
from sqlalchemy import Engine, create_engine, select, text
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from music_ingest.adapters.external.lrclib import LrclibAdapter, LrclibHttpResponse
from music_ingest.models import (
    Base,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.publication import (
    PublicationAttemptRequest,
    expose_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker
from tests.test_lrclib_handler import _published_record

pytestmark = pytest.mark.postgres


@pytest.fixture
def race_engine(monkeypatch: pytest.MonkeyPatch) -> Iterator[Engine]:
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    with PostgresContainer('postgres:17') as postgres:
        engine = create_engine(postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg'))
        Base.metadata.create_all(engine)
        with Session(engine) as session:
            now = datetime.now(UTC)
            session.add(
                SourceRootRecord(
                    id='legacy',
                    display_name='Legacy',
                    canonical_path='/legacy',
                    enabled=True,
                    scan_state='never_scanned',
                    created_at=now,
                    updated_at=now,
                )
            )
            session.commit()
        try:
            yield engine
        finally:
            engine.dispose()


@pytest.mark.parametrize('finalized', [False, True])
def test_identical_reservation_uses_fresh_state_even_with_cached_publications(
    race_engine: Engine, tmp_path: Path, finalized: bool
) -> None:
    now = datetime.now(UTC)
    target = tmp_path / 'media' / 'audio.mka'
    with Session(race_engine) as session:
        _prepare(session, tmp_path, 'original', 'record', target, now)
    with Session(race_engine) as cached:
        record = cached.get(LibraryRecord, 'record')
        assert record is not None and record.publications == []
        if finalized:
            with Session(race_engine) as recovery:
                reconcile_attempts(recovery, now, lrclib_enabled=False)
        request = PublicationAttemptRequest(
            'duplicate',
            'record',
            'original',
            None,
            target.parent,
            target.name,
            tmp_path / 'duplicate' / 'staged',
            tmp_path / 'duplicate' / 'backup',
            now,
        )
        assert reserve_attempt(cached, request) is None
        cached.commit()
        assert len(cached.scalars(select(PublicationAttemptRecord)).all()) == 1


def test_concurrent_identical_reservations_create_one_intent(race_engine: Engine, tmp_path: Path) -> None:
    now = datetime.now(UTC)
    target = tmp_path / 'media' / 'audio.mka'
    with Session(race_engine) as session:
        _prepare(session, tmp_path, 'original', 'record', target, now)
        original = session.get(PublicationAttemptRecord, 'original')
        assert original is not None
        original.state = 'failed'
        session.commit()
    ready = Barrier(2)

    def reserve(name: str) -> bool:
        with Session(race_engine) as session:
            session.execute(text("SET statement_timeout = '5s'"))
            ready.wait(timeout=5)
            attempt = reserve_attempt(
                session,
                PublicationAttemptRequest(
                    name,
                    'record',
                    'original',
                    None,
                    target.parent,
                    target.name,
                    tmp_path / name / 'staged',
                    tmp_path / name / 'backup',
                    now,
                ),
            )
            session.commit()
            return attempt is not None

    with ThreadPoolExecutor(max_workers=2) as executor:
        assert sorted(executor.map(reserve, ['first', 'second'])) == [False, True]
    with Session(race_engine) as session:
        assert (
            len(
                session.scalars(
                    select(PublicationAttemptRecord).where(PublicationAttemptRecord.state == 'reserved')
                ).all()
            )
            == 1
        )


def _prepare(session: Session, root: Path, name: str, record_id: str, target: Path, now: datetime) -> None:
    record = session.get(LibraryRecord, record_id)
    if record is None:
        record = LibraryRecord(id=record_id, created_at=now, updated_at=now)
        session.add(record)
    source = SourceRecord(
        id=name,
        source_path=str(root / f'{name}.flac'),
        device=1,
        inode=len(name),
        size_bytes=1,
        sha256=sha256(name.encode()).hexdigest(),
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        library_record=record,
    )
    session.add(source)
    staging = root / name / 'staged'
    staging.mkdir(parents=True)
    (staging / target.name).write_bytes(name.encode())
    attempt = reserve_attempt(
        session,
        PublicationAttemptRequest(
            name,
            record_id,
            name,
            None,
            target.parent,
            target.name,
            staging,
            root / name / 'backup',
            now,
        ),
    )
    mark_staged(session, attempt, now)
    attempt.state = 'prepared'
    session.commit()


@pytest.mark.parametrize('rename_before_commit', [False, True])
def test_reconcile_reaches_destination_owner_beyond_first_page(
    race_engine: Engine, tmp_path: Path, rename_before_commit: bool
) -> None:
    now = datetime.now(UTC)
    target = tmp_path / 'media' / 'audio.mka'
    with Session(race_engine) as session:
        # All timestamps tie: pagination must also advance by ID, not by offset
        # into a shrinking set of uncleaned attempts.
        for index in range(101):
            name = f'waiting-{index:03}'
            _prepare(session, tmp_path, name, name, target, now)
        _prepare(session, tmp_path, 'z-owner', 'owner-record', target, now)
        owner = session.get(PublicationAttemptRecord, 'z-owner')
        assert owner is not None
        expose_attempt(session, owner, now)
        if rename_before_commit:
            owner.state = 'prepared'
            owner.exposed_at = None
        session.commit()
        reconcile_attempts(session, now, lrclib_enabled=False)
        session.refresh(owner)
        assert owner.state == 'finalized' and owner.cleaned_at is not None
        assert target.read_bytes() == b'z-owner'
        # Deferred earlier rows get a turn on the following pass, and cleanup
        # shrinking the candidate set must not cause later rows to be skipped.
        reconcile_attempts(session, now, lrclib_enabled=False)
        attempts = session.scalars(select(PublicationAttemptRecord)).all()
        assert len(attempts) == 102
        assert all(attempt.cleaned_at is not None for attempt in attempts)
        assert all(attempt.state == 'failed' for attempt in attempts if attempt.id != 'z-owner')


def test_reconcile_defers_contended_storage_lock_and_releases_record(race_engine: Engine, tmp_path: Path) -> None:
    now = datetime.now(UTC)
    target = tmp_path / 'media' / 'audio.mka'
    with Session(race_engine) as session:
        _prepare(session, tmp_path, 'busy', 'record-busy', target, now)

    with Session(race_engine) as holder, Session(race_engine) as contender:
        holder.execute(text('SELECT pg_advisory_xact_lock(732014901)'))
        contender.execute(text("SET statement_timeout = '2s'"))
        reconcile_attempts(contender, now, lrclib_enabled=False)
        attempt = contender.get(PublicationAttemptRecord, 'busy')
        assert attempt is not None and attempt.state == 'prepared' and attempt.cleaned_at is None
        # Keep the contender open: deferral itself must release both locks,
        # rather than relying on session teardown to release them.
        with Session(race_engine) as probe:
            assert probe.scalar(select(LibraryRecord).with_for_update(nowait=True)) is not None
            assert probe.scalar(text('SELECT pg_try_advisory_xact_lock(732014902)'))
        assert not target.exists()
    with Session(race_engine) as session:
        reconcile_attempts(session, now, lrclib_enabled=False)
        attempt = session.get(PublicationAttemptRecord, 'busy')
        assert attempt is not None and attempt.state == 'finalized' and attempt.cleaned_at is not None
    assert target.read_bytes() == b'busy'


@pytest.mark.parametrize('same_record', [False, True])
@pytest.mark.parametrize('late_older', [False, True])
def test_source_scoped_recovery_reserves_destination_across_expose_commit(
    race_engine: Engine,
    tmp_path: Path,
    same_record: bool,
    late_older: bool,
) -> None:
    now = datetime.now(UTC)
    target = tmp_path / 'media' / 'audio.mka'
    target.parent.mkdir()
    target.write_bytes(b'previous')
    nfo = target.parent / 'album.nfo'
    nfo.write_bytes(b'preserve')
    with Session(race_engine) as session:
        _prepare(session, tmp_path, 'first', 'record-first', target, now)
        if not late_older:
            _prepare(
                session,
                tmp_path,
                'second',
                'record-first' if same_record else 'record-second',
                target,
                now + timedelta(seconds=1),
            )
    exposed = Event()
    resume = Event()

    def first() -> None:
        with Session(race_engine) as session:
            commit = session.commit

            def checkpoint() -> None:
                attempt = session.get(PublicationAttemptRecord, 'first')
                pause = attempt is not None and attempt.state == 'exposed'
                commit()
                if pause:
                    exposed.set()
                    assert resume.wait(10)

            session.commit = checkpoint
            reconcile_attempts(session, now, source_id='first', lrclib_enabled=False)

    with ThreadPoolExecutor(max_workers=1) as executor:
        pending = executor.submit(first)
        try:
            assert exposed.wait(10)
            with Session(race_engine) as session:
                if late_older:
                    _prepare(
                        session,
                        tmp_path,
                        'second',
                        'record-first' if same_record else 'record-second',
                        target,
                        now - timedelta(seconds=1),
                    )
                reconcile_attempts(session, now, source_id='second', lrclib_enabled=False)
                second = session.get(PublicationAttemptRecord, 'second')
                assert second is not None and second.state == 'prepared'
                assert target.read_bytes() == b'first'
        finally:
            resume.set()
        pending.result(timeout=10)
    with Session(race_engine) as session:
        reconcile_attempts(session, now, source_id='second', lrclib_enabled=False)
        publications = session.scalars(
            select(LibraryPublicationRecord).where(LibraryPublicationRecord.state == 'current')
        ).all()
        assert len(publications) == 1
        assert publications[0].source_id == ('second' if same_record else 'first')
        assert publications[0].content_sha256 == sha256(target.read_bytes()).hexdigest()
        assert all(item.cleaned_at is not None for item in session.scalars(select(PublicationAttemptRecord)))
    assert nfo.read_bytes() == b'preserve'


def test_slow_lrclib_record_is_deferred_without_blocking_independent_publication(
    race_engine: Engine,
    tmp_path: Path,
) -> None:
    entered = Event()
    release = Event()
    now = datetime.now(UTC)
    media = tmp_path / 'media'

    class SlowTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> LrclibHttpResponse:
            entered.set()
            assert release.wait(10)
            return LrclibHttpResponse(status_code=404, body=b'')

    config = ProcessingConfig(
        tmp_path / 'incoming', tmp_path / 'staging', media, lrclib_adapter=LrclibAdapter(SlowTransport())
    )
    with Session(race_engine) as session:
        record, _, publication = _published_record(session, media)
        session.flush()
        JobRepository(session).enqueue_lrclib_fetch(record.id, now)
        _prepare(session, tmp_path, 'busy', record.id, Path(publication.path), now)
        _prepare(session, tmp_path, 'independent', 'independent-record', media / 'independent.mka', now)

    def fetch() -> None:
        with Session(race_engine) as session:
            assert ProcessingWorker(session, config).run_once(allowed_kinds={'lrclib_fetch'})

    def recover() -> None:
        with Session(race_engine) as session:
            reconcile_attempts(session, now, lrclib_enabled=False)
            busy = session.get(PublicationAttemptRecord, 'busy')
            independent = session.get(PublicationAttemptRecord, 'independent')
            assert busy is not None and busy.state == 'prepared'
            assert independent is not None and independent.state == 'finalized'
            assert independent.cleaned_at is not None

    with ThreadPoolExecutor(max_workers=2) as executor:
        fetching = executor.submit(fetch)
        try:
            assert entered.wait(10)
            executor.submit(recover).result(timeout=5)
            with Session(race_engine) as probe:
                assert probe.scalar(text('SELECT pg_try_advisory_xact_lock(732014901)'))
                assert not probe.scalar(text('SELECT pg_try_advisory_xact_lock(732014902)'))
        finally:
            release.set()
        fetching.result(timeout=10)
    with Session(race_engine) as session:
        reconcile_attempts(session, now, lrclib_enabled=False)
        busy = session.get(PublicationAttemptRecord, 'busy')
        assert busy is not None and busy.state == 'finalized' and busy.cleaned_at is not None
