from __future__ import annotations

from pathlib import Path

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, inspect, text
from sqlalchemy.exc import IntegrityError

from alembic import command
from tests.support.paths import ALEMBIC_DIRECTORY

_PREVIOUS_REVISION = '20260916_0029'
_OBSERVED_AT = '2026-09-23 00:00:00'


def test_musicbrainz_identity_pair_migration_clears_partial_rows_and_enforces_pair(tmp_path: Path) -> None:
    # Given: legacy records contain valid, absent, and partial MusicBrainz identities.
    database_path = tmp_path / 'musicbrainz-pair.db'
    config = Config()
    config.set_main_option('script_location', str(ALEMBIC_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')
    engine = create_engine(f'sqlite+pysqlite:///{database_path}')
    command.upgrade(config, _PREVIOUS_REVISION)
    with engine.begin() as connection:
        for record_id, recording_mbid, release_mbid, match_state in (
            ('complete', 'recording-complete', 'release-complete', 'matched'),
            ('absent', None, None, 'unmatched'),
            ('recording-only', 'recording-partial', None, 'matched'),
            ('release-only', None, 'release-partial', 'matched'),
            ('blank-recording', '', 'release-blank', 'matched'),
        ):
            _ = connection.execute(
                text(
                    ' '.join(
                        (
                            'INSERT INTO library_records',
                            '(id, musicbrainz_recording_id, musicbrainz_release_id, source_state, processing_state,',
                            'match_state, publication_state, metadata_state, created_at, updated_at)',
                            'VALUES (:id, :recording_mbid, :release_mbid, :source_state, :processing_state,',
                            ':match_state, :publication_state, :metadata_state, :created_at, :updated_at)',
                        )
                    )
                ),
                {
                    'id': record_id,
                    'recording_mbid': recording_mbid,
                    'release_mbid': release_mbid,
                    'source_state': 'present',
                    'processing_state': 'complete',
                    'match_state': match_state,
                    'publication_state': 'current',
                    'metadata_state': 'final',
                    'created_at': _OBSERVED_AT,
                    'updated_at': _OBSERVED_AT,
                },
            )

    # When: the pair-invariant migration reaches head.
    command.upgrade(config, 'head')

    # Then: only complete pairs remain confirmed and every repaired row is auditable.
    with engine.connect() as connection:
        rows = connection.execute(
            text(
                ' '.join(
                    (
                        'SELECT id, musicbrainz_recording_id, musicbrainz_release_id, match_state, processing_state',
                        'FROM library_records ORDER BY id',
                    )
                )
            )
        ).all()
        assert rows == [
            ('absent', None, None, 'unmatched', 'complete'),
            ('blank-recording', None, None, 'needs_review', 'needs_review'),
            ('complete', 'recording-complete', 'release-complete', 'matched', 'complete'),
            ('recording-only', None, None, 'needs_review', 'needs_review'),
            ('release-only', None, None, 'needs_review', 'needs_review'),
        ]
        assert (
            connection.scalar(
                text("SELECT count(*) FROM library_events WHERE kind = 'musicbrainz_pair_migration_review'")
            )
            == 3
        )

    constraints = {item['name'] for item in inspect(engine).get_check_constraints('library_records')}
    assert {'ck_library_records_musicbrainz_pair', 'ck_library_records_matched_identity'} <= constraints
    with pytest.raises(IntegrityError), engine.begin() as connection:
        _ = connection.execute(
            text("UPDATE library_records SET musicbrainz_recording_id = 'recording-partial' WHERE id = 'absent'")
        )
    engine.dispose()
