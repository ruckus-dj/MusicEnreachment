from __future__ import annotations

from pathlib import Path

from alembic.config import Config
from alembic.runtime.migration import MigrationContext
from sqlalchemy import create_engine, inspect

from alembic import command

_MIGRATION_DIRECTORY = Path(__file__).parents[1] / 'alembic'
_HEAD_REVISION = '20260729_0010'
_APPLICATION_TABLES = frozenset(
    {
        'source_records',
        'source_tag_observations',
        'artwork_hash_observations',
        'provider_attempts',
        'candidate_evidence',
        'review_decisions',
        'publication_records',
        'fingerprint_evidence',
        'provider_snapshots',
        'provider_schedules',
        'review_releases',
        'review_audits',
        'publish_snapshots',
        'webhook_receipts',
        'release_groups',
        'releases',
        'tracks',
        'release_files',
        'tag_layers',
        'jobs',
        'job_attempts',
        'publication_states',
        'tombstones',
        'audit_records',
    }
)


def test_migration_lineage_when_upgraded_and_downgraded_preserves_schema_boundaries(tmp_path: Path) -> None:
    # Given: an isolated SQLite database configured with the repository's real Alembic lineage.
    database_path = tmp_path / 'migration-lineage.db'
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')

    try:
        # When: the complete lineage reaches head, then returns to base.
        command.upgrade(config, 'head')
        with engine.connect() as connection:
            revision = MigrationContext.configure(connection).get_current_revision()
        upgraded_tables = frozenset(inspect(engine).get_table_names())
        command.downgrade(config, 'base')
        downgraded_tables = frozenset(inspect(engine).get_table_names())

        # Then: head exposes every application table and base removes them all.
        assert revision == _HEAD_REVISION
        assert upgraded_tables >= _APPLICATION_TABLES
        assert not _APPLICATION_TABLES.intersection(downgraded_tables)
    finally:
        engine.dispose()
