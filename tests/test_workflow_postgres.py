from __future__ import annotations

import os
from pathlib import Path

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, text
from sqlalchemy.exc import ProgrammingError

from alembic import command

_POSTGRES_URL_ENVIRONMENT = 'MUSIC_INGEST_POSTGRES_TEST_URL'


@pytest.mark.live
def test_workflow_migration_when_postgres_test_database_is_configured_enforces_history_invariants() -> None:
    # Given: a dedicated disposable PostgreSQL database URL.
    database_url = os.environ.get(_POSTGRES_URL_ENVIRONMENT)
    if database_url is None:
        pytest.skip(f'{_POSTGRES_URL_ENVIRONMENT} is required for PostgreSQL migration verification')
    config = Config()
    config.set_main_option('script_location', str(Path(__file__).parents[1] / 'alembic'))
    config.set_main_option('sqlalchemy.url', database_url)
    engine = create_engine(database_url)
    command.downgrade(config, 'base')
    command.upgrade(config, 'head')

    try:
        with engine.begin() as connection:
            _ = connection.execute(text("INSERT INTO release_groups (id, title) VALUES ('group-1', 'Fixture Group')"))
            _ = connection.execute(
                text("INSERT INTO releases (id, release_group_id, title) VALUES ('release-1', 'group-1', 'Fixture')")
            )
            _ = connection.execute(
                text(
                    'INSERT INTO tracks (id, release_id, position, title) '
                    + "VALUES ('track-1', 'release-1', 1, 'Fixture')"
                )
            )
            _ = connection.execute(
                text(
                    'INSERT INTO release_files (id, track_id, relative_path, content_sha256) '
                    + "VALUES ('file-1', 'track-1', 'Fixture/01.flac', :content_sha256)"
                ),
                {'content_sha256': 'a' * 64},
            )
            _ = connection.execute(
                text(
                    'INSERT INTO audit_records (release_id, action, actor, details_json, recorded_at) '
                    + "VALUES ('release-1', 'published', 'worker', '{}', now())"
                )
            )
            _ = connection.execute(
                text(
                    'INSERT INTO tombstones (release_file_id, reason, recorded_at) '
                    + "VALUES ('file-1', 'superseded', now())"
                )
            )

        # When: PostgreSQL receives an invalid revision or mutation of immutable history.
        with engine.begin() as connection, pytest.raises(ProgrammingError):
            _ = connection.execute(
                text(
                    'INSERT INTO tag_layers (release_file_id, layer, revision, tags_json, recorded_at) '
                    + "VALUES ('file-1', 'final', 0, '{}', now())"
                )
            )

        # Then: the database rejects direct rewrites and deletion of append-only records.
        with engine.begin() as connection, pytest.raises(ProgrammingError):
            _ = connection.execute(text("UPDATE audit_records SET action = 'rewritten' WHERE id = 1"))
        with engine.begin() as connection, pytest.raises(ProgrammingError):
            _ = connection.execute(text("DELETE FROM tombstones WHERE release_file_id = 'file-1'"))
    finally:
        command.downgrade(config, 'base')
        engine.dispose()
