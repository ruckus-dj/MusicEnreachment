from datetime import UTC, datetime, timedelta

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.dto.settings import RuntimeSettings
from music_ingest.models import Base, JobRecord
from music_ingest.models.jobs import JobRepository
from music_ingest.settings import build_runtime_settings, save_runtime_settings

DEFAULT_POOLS = {
    'filesystem_scan': 4,
    'acoustid_analysis': 1,
    'musicbrainz_analysis': 4,
    'candidate_selection': 4,
    'folder_release_selection': 2,
    'final_publish': 4,
    'selection_refresh': 4,
    'lrclib_fetch': 1,
    'artwork_enrichment': 2,
    'reconciliation_scan': 1,
}


def test_independent_pool_defaults_and_persistence() -> None:
    settings = RuntimeSettings()
    assert settings.worker_pools.model_dump() == DEFAULT_POOLS
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        changed = settings.model_copy(
            update={'worker_pools': settings.worker_pools.model_copy(update={'final_publish': 7})}
        )
        save_runtime_settings(session, changed)
        session.commit()
    with Session(engine) as session:
        assert build_runtime_settings(session).worker_pools.model_dump() == {**DEFAULT_POOLS, 'final_publish': 7}


def test_migration_adds_dedicated_pools_without_losing_legacy_capacity_on_downgrade() -> None:
    from runpy import run_path

    from alembic.migration import MigrationContext
    from alembic.operations import Operations
    from sqlalchemy import text

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with engine.begin() as connection:
        connection.execute(
            text(
                'INSERT INTO runtime_settings (key, value, updated_at) '
                "VALUES ('processing.worker_concurrency', '8', CURRENT_TIMESTAMP)"
            )
        )
        migration = run_path('alembic/versions/20260914_0025_dedicated_worker_pools.py')
        with Operations.context(MigrationContext.configure(connection)):
            migration['upgrade']()
        legacy_concurrency = text("SELECT value FROM runtime_settings WHERE key='processing.worker_concurrency'")
        assert connection.scalar(legacy_concurrency) == '8'
        with Operations.context(MigrationContext.configure(connection)):
            migration['downgrade']()
        assert connection.scalar(legacy_concurrency) == '8'
        assert connection.scalar(text("SELECT value FROM runtime_settings WHERE key='processing.worker_pools'")) is None
    with Session(engine) as session:
        assert build_runtime_settings(session).worker_pools.model_dump() == DEFAULT_POOLS


def test_migration_removes_obsolete_provider_pool_from_saved_settings() -> None:
    from runpy import run_path

    from alembic.migration import MigrationContext
    from alembic.operations import Operations
    from sqlalchemy import text

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with engine.begin() as connection:
        connection.execute(
            text(
                'INSERT INTO runtime_settings (key, value, updated_at) '
                "VALUES ('processing.worker_pools', :value, CURRENT_TIMESTAMP)"
            ),
            {'value': '{"filesystem_scan": 4, "lidarr_intake": 1}'},
        )
        migration = run_path('alembic/versions/20260915_0026_remove_provider_intake.py')
        with Operations.context(MigrationContext.configure(connection)):
            migration['upgrade']()
        stored = connection.scalar(text("SELECT value FROM runtime_settings WHERE key='processing.worker_pools'"))

    assert stored == '{"filesystem_scan": 4}'


def test_pool_claim_never_borrows_other_kinds() -> None:
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(JobRecord(id='scan', source_id='source', kind='filesystem_scan', state='queued', created_at=now))
        session.commit()
        assert JobRepository(session).claim_next(now, timedelta(minutes=5), allowed_kinds={'acoustid_analysis'}) is None
        claimed = JobRepository(session).claim_next(now, timedelta(minutes=5), allowed_kinds={'filesystem_scan'})
        assert claimed is not None
        assert claimed.job.id == 'scan'
