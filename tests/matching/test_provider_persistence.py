from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import override

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, inspect, select
from sqlalchemy.dialects import postgresql
from sqlalchemy.orm import Session

from alembic import command
from music_ingest.models import Base, ProviderScheduleRecord, ProviderSnapshotRecord
from music_ingest.repositories.persistence import ProviderPersistenceRepository, ensure_provider_schedules
from music_ingest.services.matching.providers import DatabaseRequestRateLimiter
from tests.support.paths import ALEMBIC_DIRECTORY


def test_provider_snapshot_is_global_append_only_and_lookup_is_newest(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    captured_at = datetime(2026, 7, 28, tzinfo=UTC)

    with Session(engine) as session:
        repository = ProviderPersistenceRepository(session)
        first = repository.append_snapshot(
            provider_name='musicbrainz',
            request_hash='a' * 64,
            request_descriptor='GET recording lookup; query=artist/title',
            response_sha256='b' * 64,
            response_body=b'{"release": "fixture"}',
            captured_at=captured_at,
            outcome='success',
            state='complete',
            http_status=200,
        )
        second = repository.append_snapshot(
            provider_name='musicbrainz',
            request_hash='a' * 64,
            request_descriptor='GET recording lookup; query=artist/title',
            response_sha256='c' * 64,
            response_body=None,
            captured_at=captured_at + timedelta(seconds=1),
            outcome='success',
            state='complete',
        )
        first_id, second_id = first.id, second.id
        assert first_id != second_id
        session.commit()

    assert first_id != second_id
    with Session(engine) as session:
        repository = ProviderPersistenceRepository(session)
        newest = repository.newest_relevant('musicbrainz', 'a' * 64)
        assert newest is not None
        assert newest.response_sha256 == 'c' * 64
        assert newest.response_body is None
        assert session.scalar(select(ProviderSnapshotRecord).where(ProviderSnapshotRecord.response_sha256 == 'b' * 64))
        stored = session.scalar(
            select(ProviderSnapshotRecord).where(ProviderSnapshotRecord.response_sha256 == 'b' * 64)
        )
        assert stored is not None
        assert stored.response_body == b'{"release": "fixture"}'


def test_provider_schedule_reservation_flushes_without_commit_and_returns_lease(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 7, 28, tzinfo=UTC)

    with Session(engine) as session:
        session.add(ProviderScheduleRecord(provider_name='acoustid', next_start_at=now))
        session.commit()
        repository = ProviderPersistenceRepository(session)
        reserved = repository.reserve_next_start('acoustid', now, timedelta(seconds=10), timedelta(seconds=30))
        assert reserved.scheduled_start == now
        assert reserved.lease_token
        assert reserved.lease_until == now + timedelta(seconds=30)
        assert repository.validate_lease('acoustid', reserved.lease_token, now)
        assert not repository.validate_lease('acoustid', 'wrong-token', now)
        schedule = session.get(ProviderScheduleRecord, 'acoustid')
        assert schedule is not None
        assert schedule.next_start_at.replace(tzinfo=UTC) == now + timedelta(seconds=10)
        session.rollback()

    with Session(engine) as session:
        assert session.get(ProviderScheduleRecord, 'acoustid') is not None


def test_database_rate_limiter_when_called_twice_reserves_one_and_a_half_seconds(tmp_path: Path) -> None:
    # Given: a shared database schedule and a sleep seam that records requested delays.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=datetime.now(UTC)))
        session.commit()
    delays: list[float] = []
    limiter = DatabaseRequestRateLimiter(lambda: Session(engine), delays.append)

    # When: two MusicBrainz wire calls reserve their start times.
    limiter.wait('musicbrainz')
    limiter.wait('musicbrainz')

    # Then: the second call is scheduled at the configured 0.5-0.7 RPS interval.
    assert delays
    assert 1.4 <= delays[-1] <= 1.6


def test_database_rate_limiter_when_acoustid_is_called_twice_reserves_one_third_second(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: the official AcoustID provider schedule and no operator override.
    now = datetime(2026, 9, 25, tzinfo=UTC)

    class FixedDateTime(datetime):
        @override
        @classmethod
        def now(cls, tz: object = None) -> datetime:
            _ = tz
            return now

    monkeypatch.setattr('music_ingest.services.matching.providers.datetime', FixedDateTime)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(ProviderScheduleRecord(provider_name='acoustid', next_start_at=now))
        session.commit()
    delays: list[float] = []
    limiter = DatabaseRequestRateLimiter(lambda: Session(engine), delays.append)

    # When: two AcoustID calls reserve starts through the durable limiter.
    limiter.wait('acoustid')
    limiter.wait('acoustid')

    # Then: the second call cannot exceed AcoustID's three-requests-per-second policy.
    assert delays
    assert 0.3 <= delays[-1] <= 0.4


def test_database_rate_limiter_when_custom_interval_is_configured_reserves_that_interval(tmp_path: Path) -> None:
    # Given: a shared schedule and a self-hosted MusicBrainz interval with no delay.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=datetime.now(UTC)))
        session.commit()
    delays: list[float] = []
    limiter = DatabaseRequestRateLimiter(lambda: Session(engine), delays.append, timedelta())

    # When: two requests reserve their starts.
    limiter.wait('musicbrainz')
    limiter.wait('musicbrainz')

    # Then: the configured zero-delay interval does not throttle either request.
    assert delays == []


def test_database_rate_limiter_when_configured_interval_is_zero_skips_session_creation() -> None:
    # Given: a configured zero interval and a session factory that must not be entered.
    def fail_if_session_is_created() -> Session:
        raise AssertionError('zero interval should bypass the database limiter')

    limiter = DatabaseRequestRateLimiter(fail_if_session_is_created, request_interval=timedelta())

    # When: a MusicBrainz request enters the limiter.
    limiter.wait('musicbrainz')

    # Then: no database session or sleep is required.


def test_repository_rejects_naive_provider_timestamps(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        repository = ProviderPersistenceRepository(session)
        with pytest.raises(ValueError, match='UTC-aware'):
            _ = repository.append_snapshot(
                provider_name='musicbrainz',
                request_hash='a' * 64,
                request_descriptor='GET recording lookup',
                response_sha256='b' * 64,
                captured_at=datetime(2026, 7, 28),
                outcome='success',
                state='complete',
            )
        session.add(
            ProviderScheduleRecord(
                provider_name='musicbrainz',
                next_start_at=datetime(2026, 7, 28, tzinfo=UTC),
            )
        )
        session.commit()
        with pytest.raises(ValueError, match='UTC-aware'):
            _ = repository.reserve_next_start(
                'musicbrainz',
                datetime(2026, 7, 28),
                timedelta(seconds=1),
                timedelta(seconds=1),
            )


def test_schedule_reservation_statement_compiles_to_postgresql_for_update() -> None:
    statement = (
        select(ProviderScheduleRecord)
        .where(
            ProviderScheduleRecord.provider_name == 'musicbrainz',
        )
        .with_for_update()
    )
    assert 'FOR UPDATE' in str(statement.compile(dialect=postgresql.dialect()))


def test_provider_models_have_no_source_or_sensitive_snapshot_columns() -> None:
    snapshot_columns = set(ProviderSnapshotRecord.__table__.columns.keys())
    assert 'source_id' not in snapshot_columns
    assert not snapshot_columns.intersection({'user_agent', 'contact', 'acoustid_key', 'fingerprint', 'exception'})
    assert 'response_body' in snapshot_columns


def test_runtime_initialization_seeds_provider_schedules_idempotently(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "provider.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 7, 28, tzinfo=UTC)

    with Session(engine) as session:
        ensure_provider_schedules(session, ('musicbrainz', 'acoustid'), now)
        session.commit()

    with Session(engine) as session:
        ensure_provider_schedules(session, ('musicbrainz', 'acoustid'), now + timedelta(minutes=1))
        session.commit()
        schedules = session.scalars(select(ProviderScheduleRecord)).all()
        assert {schedule.provider_name for schedule in schedules} == {'musicbrainz', 'acoustid'}
        assert {schedule.next_start_at.replace(tzinfo=UTC) for schedule in schedules} == {now}


def test_provider_migration_creates_schedule_tables_without_runtime_rows(tmp_path: Path) -> None:
    database_path = tmp_path / 'migration.db'
    config = Config()
    config.set_main_option('script_location', str(ALEMBIC_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    command.upgrade(config, 'head')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')
    assert {'provider_snapshots', 'provider_schedules'} <= set(inspect(engine).get_table_names())
    assert 'response_body' in {column['name'] for column in inspect(engine).get_columns('provider_snapshots')}
    with Session(engine) as session:
        assert session.scalars(select(ProviderScheduleRecord)).all() == []
    command.downgrade(config, 'base')
    assert 'provider_snapshots' not in inspect(engine).get_table_names()
