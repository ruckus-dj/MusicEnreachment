from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest
from alembic.config import Config
from sqlalchemy import Engine, create_engine, text
from sqlalchemy.exc import IntegrityError

from alembic import command

_RELEASE_INSERT = (
    'INSERT INTO releases (id, release_group_id, title) ' + "VALUES ('release-1', 'group-1', 'Fixture Release')"
)
_TRACK_INSERT = (
    'INSERT INTO tracks (id, release_id, position, title) ' + "VALUES ('track-1', 'release-1', 1, 'Fixture Track')"
)


def _migrated_engine(tmp_path: Path) -> Engine:
    database_path = tmp_path / 'workflow.db'
    config = Config()
    config.set_main_option('script_location', str(Path(__file__).parents[1] / 'alembic'))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    command.upgrade(config, 'head')
    return create_engine(f'sqlite+pysqlite:///{database_path}')


def _insert_release_file(engine: Engine) -> None:
    with engine.begin() as connection:
        _ = connection.execute(text("INSERT INTO release_groups (id, title) VALUES ('group-1', 'Fixture Group')"))
        _ = connection.execute(text(_RELEASE_INSERT))
        _ = connection.execute(text(_TRACK_INSERT))
        _ = connection.execute(
            text(
                'INSERT INTO release_files (id, track_id, relative_path, content_sha256) '
                + "VALUES ('file-1', 'track-1', 'Fixture/01.flac', :content_sha256)"
            ),
            {'content_sha256': 'a' * 64},
        )


def test_tag_layer_when_revision_is_not_greater_than_existing_revision_is_rejected(tmp_path: Path) -> None:
    # Given: a tag layer with revisions one and three already persisted.
    engine = _migrated_engine(tmp_path)
    _insert_release_file(engine)
    with engine.begin() as connection:
        _ = connection.execute(
            text(
                'INSERT INTO tag_layers (release_file_id, layer, revision, tags_json, recorded_at) '
                + "VALUES ('file-1', 'original', 1, '{}', '2026-07-29T00:00:00+00:00')"
            )
        )
        _ = connection.execute(
            text(
                'INSERT INTO tag_layers (release_file_id, layer, revision, tags_json, recorded_at) '
                + "VALUES ('file-1', 'original', 3, '{}', '2026-07-29T00:00:00+00:00')"
            )
        )

    # When: a stale revision is inserted with a new unique revision number.
    # Then: persistence rejects the non-monotonic write rather than accepting a stale history entry.
    with engine.begin() as connection, pytest.raises(IntegrityError):
        _ = connection.execute(
            text(
                'INSERT INTO tag_layers (release_file_id, layer, revision, tags_json, recorded_at) '
                + "VALUES ('file-1', 'original', 2, '{}', '2026-07-29T00:00:00+00:00')"
            )
        )


def test_tag_layer_when_revision_is_not_positive_is_rejected(tmp_path: Path) -> None:
    # Given: a persisted release file without tag-layer history.
    engine = _migrated_engine(tmp_path)
    _insert_release_file(engine)

    # When: revision zero is inserted.
    # Then: persistence rejects the invalid revision before it becomes history.
    with engine.begin() as connection, pytest.raises(IntegrityError):
        _ = connection.execute(
            text(
                'INSERT INTO tag_layers (release_file_id, layer, revision, tags_json, recorded_at) '
                + "VALUES ('file-1', 'original', 0, '{}', '2026-07-29T00:00:00+00:00')"
            )
        )


def test_audit_record_when_updated_is_rejected_as_append_only_history(tmp_path: Path) -> None:
    # Given: one persisted audit record.
    engine = _migrated_engine(tmp_path)
    _insert_release_file(engine)
    with engine.begin() as connection:
        _ = connection.execute(
            text(
                'INSERT INTO audit_records (release_id, action, actor, details_json, recorded_at) '
                + "VALUES ('release-1', 'published', 'worker', '{}', '2026-07-29T00:00:00+00:00')"
            )
        )

    # When: a direct update attempts to rewrite the audit action.
    # Then: persistence rejects the rewrite.
    with engine.begin() as connection, pytest.raises(IntegrityError):
        _ = connection.execute(text("UPDATE audit_records SET action = 'rewritten' WHERE id = 1"))


def test_tombstone_when_deleted_is_rejected_as_append_only_history(tmp_path: Path) -> None:
    # Given: a persisted tombstone.
    engine = _migrated_engine(tmp_path)
    _insert_release_file(engine)
    with engine.begin() as connection:
        _ = connection.execute(
            text(
                'INSERT INTO tombstones (release_file_id, reason, recorded_at) '
                + "VALUES ('file-1', 'superseded', '2026-07-29T00:00:00+00:00')"
            )
        )

    # When: a direct delete attempts to erase the tombstone.
    # Then: persistence rejects the destructive write.
    with engine.begin() as connection, pytest.raises(IntegrityError):
        _ = connection.execute(text("DELETE FROM tombstones WHERE release_file_id = 'file-1'"))


def test_alembic_cli_when_invoked_without_pythonpath_imports_application_models(tmp_path: Path) -> None:
    # Given: a temporary CLI configuration with an SQLite database and no PYTHONPATH override.
    root = Path(__file__).parents[1]
    database_path = tmp_path / 'workflow.db'
    config_path = tmp_path / 'alembic.ini'
    _ = config_path.write_text(
        (root / 'alembic.ini')
        .read_text(encoding='utf-8')
        .replace('script_location = alembic', f'script_location = {root / "alembic"}')
        .replace(
            'postgresql+psycopg://music_ingest:placeholder@localhost/music_ingest',
            f'sqlite+pysqlite:///{database_path}',
        ),
        encoding='utf-8',
    )
    environment = dict(os.environ)
    _ = environment.pop('PYTHONPATH', None)

    # When: the supported Alembic CLI runs from outside the repository root.
    result = subprocess.run(  # noqa: S603
        [sys.executable, '-m', 'alembic', '-c', str(config_path), 'upgrade', 'head'],
        cwd=tmp_path,
        env=environment,
        check=False,
        capture_output=True,
        text=True,
    )

    # Then: migration succeeds and does not require an ambient source-path setting.
    assert result.returncode == 0, result.stderr
