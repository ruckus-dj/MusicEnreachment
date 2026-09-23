from __future__ import annotations

from pathlib import Path

import pytest
from alembic.config import Config
from alembic.runtime.migration import MigrationContext
from sqlalchemy import create_engine, inspect, text
from sqlalchemy.exc import IntegrityError

from alembic import command
from tests.support.paths import ALEMBIC_DIRECTORY

_MIGRATION_DIRECTORY = ALEMBIC_DIRECTORY
_HEAD_REVISION = '20260923_0030'
_PREVIOUS_REVISION = '20260909_0023'
_OBSERVED_AT = '2026-09-11 00:00:00'
_LYRIC_STATE_COLUMNS = frozenset(
    {
        'lyrics_status',
        'lyrics_path',
        'lyrics_publication_id',
        'lyrics_sha256',
        'lyrics_updated_at',
        'lyrics_evidence_json',
    }
)
_LRCLIB_FETCH_INDEX = 'uq_active_lrclib_fetch_job'
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


def test_lyric_state_migration_when_upgraded_materializes_state_for_existing_records(tmp_path: Path) -> None:
    # Given: a database at the revision before lyric state, holding one existing library record.
    database_path = tmp_path / 'lyric-state-upgrade.db'
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')

    try:
        command.upgrade(config, _PREVIOUS_REVISION)
        with engine.begin() as connection:
            _ = connection.execute(
                text(
                    'INSERT INTO library_records '
                    '(id, source_state, processing_state, match_state, publication_state, metadata_state, '
                    'created_at, updated_at) '
                    "VALUES ('record-existing', 'present', 'queued', 'unmatched', 'absent', 'original', "
                    ':observed_at, :observed_at)'
                ),
                {'observed_at': _OBSERVED_AT},
            )
        previous_columns = {column['name'] for column in inspect(engine).get_columns('library_records')}

        # When: the lyric-state migration reaches head.
        command.upgrade(config, 'head')
        columns = {column['name'] for column in inspect(engine).get_columns('library_records')}

        # Then: the new lyric columns materialize current state without storing any lyric text.
        assert _LYRIC_STATE_COLUMNS.isdisjoint(previous_columns)
        assert _LYRIC_STATE_COLUMNS.issubset(columns)
        assert _LRCLIB_FETCH_INDEX in {index['name'] for index in inspect(engine).get_indexes('jobs')}
        with engine.connect() as connection:
            assert connection.execute(text('SELECT version_num FROM alembic_version')).scalar_one() == _HEAD_REVISION
            assert connection.execute(
                text(
                    'SELECT lyrics_status, lyrics_path, lyrics_publication_id, lyrics_sha256, lyrics_updated_at '
                    "FROM library_records WHERE id = 'record-existing'"
                )
            ).one() == ('none', None, None, None, None)
            assert (
                connection.execute(
                    text("SELECT lyrics_evidence_json FROM library_records WHERE id = 'record-existing'")
                ).scalar_one()
                is None
            )

        # Then: only the declared lyric states are accepted.
        with pytest.raises(IntegrityError), engine.begin() as connection:
            _ = connection.execute(
                text("UPDATE library_records SET lyrics_status = 'unknown' WHERE id = 'record-existing'")
            )
    finally:
        engine.dispose()


def test_lyric_state_migration_when_downgraded_restores_previous_record_schema(tmp_path: Path) -> None:
    # Given: a database upgraded to the lyric-state head.
    database_path = tmp_path / 'lyric-state-downgrade.db'
    config = Config()
    config.set_main_option('script_location', str(_MIGRATION_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')

    try:
        command.upgrade(config, 'head')

        # When: the migration is rolled back to its parent revision.
        command.downgrade(config, _PREVIOUS_REVISION)
        columns = {column['name'] for column in inspect(engine).get_columns('library_records')}
        indexes = {index['name'] for index in inspect(engine).get_indexes('library_records')}
        unique_columns = {
            tuple(constraint['column_names'])
            for constraint in inspect(engine).get_unique_constraints('library_records')
        }
        constraint_names = {
            constraint['name'] for constraint in inspect(engine).get_check_constraints('library_records')
        }
        foreign_key_names = {key['name'] for key in inspect(engine).get_foreign_keys('library_records')}
        with engine.connect() as connection:
            revision = connection.execute(text('SELECT version_num FROM alembic_version')).scalar_one()

        # Then: lyric state disappears while the pre-existing record constraints and indexes survive.
        assert revision == _PREVIOUS_REVISION
        assert _LYRIC_STATE_COLUMNS.isdisjoint(columns)
        assert _LRCLIB_FETCH_INDEX not in {index['name'] for index in inspect(engine).get_indexes('jobs')}
        assert 'ck_library_records_lyrics_status' not in constraint_names
        assert 'fk_library_records_lyrics_publication' not in foreign_key_names
        assert 'ix_library_records_release_publication' in indexes
        assert ('musicbrainz_recording_id', 'musicbrainz_release_id') in unique_columns

        # Then: the migration applies again cleanly on the rewritten table.
        command.upgrade(config, 'head')
        assert _LYRIC_STATE_COLUMNS.issubset(
            {column['name'] for column in inspect(engine).get_columns('library_records')}
        )
    finally:
        engine.dispose()
