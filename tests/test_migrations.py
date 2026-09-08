from __future__ import annotations

from pathlib import Path

from alembic.config import Config
from alembic.runtime.migration import MigrationContext
from sqlalchemy import create_engine, inspect

from alembic import command

_MIGRATION_DIRECTORY = Path(__file__).parents[1] / 'alembic'
_HEAD_REVISION = '20260909_0021'
_APPLICATION_TABLES = frozenset(
    {
        'source_records',
        'source_tag_observations',
        'artwork_hash_observations',
        'release_artwork',
        'provider_attempts',
        'provider_candidate_runs',
        'candidate_evidence',
        'review_decisions',
        'fingerprint_evidence',
        'decoder_evidence',
        'provider_snapshots',
        'provider_schedules',
        'webhook_receipts',
        'jobs',
        'job_attempts',
        'runtime_settings',
        'genre_catalog',
        'library_records',
        'library_record_consolidations',
        'library_metadata_revisions',
        'library_publications',
        'library_events',
        'source_roots',
        'source_recording_assignments',
        'source_association_overrides',
        'effective_source_decisions',
        'publication_attempts',
        'storage_config',
        'unsorted_filename_counters',
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


def test_baseline_migration_when_upgraded_exposes_existing_source_lineage(tmp_path: Path) -> None:
    # Given: an isolated database configured with the original migration lineage.
    database_path = tmp_path / 'baseline-lineage.db'
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')

    try:
        # When: the original head revision is applied.
        command.upgrade(config, _HEAD_REVISION)

        # Then: source provenance still owns its stable source and record linkage fields.
        columns = {column['name'] for column in inspect(engine).get_columns('source_records')}
        assert {'id', 'source_path', 'library_record_id', 'sha256', 'mtime_ns'}.issubset(columns)
    finally:
        engine.dispose()
