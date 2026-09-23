"""Enforce complete MusicBrainz identity pairs and snapshot refresh jobs."""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260923_0030'
down_revision = '20260916_0029'
branch_labels = None
depends_on = None

_IDENTITY_PAIR_CONSTRAINT = (
    '(musicbrainz_recording_id IS NULL AND musicbrainz_release_id IS NULL) OR '
    "(musicbrainz_recording_id IS NOT NULL AND trim(musicbrainz_recording_id) <> '' AND "
    "musicbrainz_release_id IS NOT NULL AND trim(musicbrainz_release_id) <> '')"
)
_MATCHED_IDENTITY_CONSTRAINT = (
    "match_state <> 'matched' OR (musicbrainz_recording_id IS NOT NULL AND musicbrainz_release_id IS NOT NULL)"
)
_JOB_IDENTITY_PAIR_CONSTRAINT = (
    '(expected_musicbrainz_recording_id IS NULL AND expected_musicbrainz_release_id IS NULL) OR '
    "(expected_musicbrainz_recording_id IS NOT NULL AND trim(expected_musicbrainz_recording_id) <> '' AND "
    "expected_musicbrainz_release_id IS NOT NULL AND trim(expected_musicbrainz_release_id) <> '')"
)


def upgrade() -> None:
    bind = op.get_bind()
    records = sa.table(
        'library_records',
        sa.column('id', sa.Text()),
        sa.column('musicbrainz_recording_id', sa.Text()),
        sa.column('musicbrainz_release_id', sa.Text()),
        sa.column('match_state', sa.Text()),
        sa.column('processing_state', sa.Text()),
        sa.column('updated_at', sa.DateTime(timezone=True)),
    )
    events = sa.table(
        'library_events',
        sa.column('library_record_id', sa.Text()),
        sa.column('source_id', sa.Text()),
        sa.column('kind', sa.Text()),
        sa.column('state', sa.Text()),
        sa.column('reason', sa.Text()),
        sa.column('details_json', sa.Text()),
        sa.column('created_at', sa.DateTime(timezone=True)),
    )
    recording_id = records.c.musicbrainz_recording_id
    release_id = records.c.musicbrainz_release_id
    complete_pair = sa.or_(
        sa.and_(recording_id.is_(None), release_id.is_(None)),
        sa.and_(
            recording_id.is_not(None),
            sa.func.trim(recording_id) != '',
            release_id.is_not(None),
            sa.func.trim(release_id) != '',
        ),
    )
    repair_predicate = sa.or_(
        sa.not_(complete_pair),
        sa.and_(records.c.match_state == 'matched', recording_id.is_(None), release_id.is_(None)),
    )
    details = (
        sa.cast(
            sa.func.json_build_object('legacy_recording_mbid', recording_id, 'legacy_release_mbid', release_id),
            sa.Text(),
        )
        if bind.dialect.name == 'postgresql'
        else sa.func.json_object('legacy_recording_mbid', recording_id, 'legacy_release_mbid', release_id)
    )
    op.execute(
        sa.insert(events).from_select(
            (
                events.c.library_record_id,
                events.c.source_id,
                events.c.kind,
                events.c.state,
                events.c.reason,
                events.c.details_json,
                events.c.created_at,
            ),
            sa.select(
                records.c.id,
                sa.null(),
                sa.literal('musicbrainz_pair_migration_review'),
                sa.literal('needs_review'),
                sa.literal('incomplete legacy MusicBrainz identity was removed; current publication was retained'),
                details,
                sa.func.current_timestamp(),
            ).where(repair_predicate),
        )
    )
    op.execute(
        sa.update(records)
        .where(repair_predicate)
        .values(
            musicbrainz_recording_id=None,
            musicbrainz_release_id=None,
            match_state='needs_review',
            processing_state='needs_review',
            updated_at=sa.func.current_timestamp(),
        )
    )

    if bind.dialect.name == 'postgresql':
        op.drop_index('uq_library_records_musicbrainz_recording_without_release', table_name='library_records')
    with op.batch_alter_table('library_records') as batch:
        batch.create_check_constraint('ck_library_records_musicbrainz_pair', _IDENTITY_PAIR_CONSTRAINT)
        batch.create_check_constraint('ck_library_records_matched_identity', _MATCHED_IDENTITY_CONSTRAINT)
    with op.batch_alter_table('jobs') as batch:
        batch.add_column(sa.Column('expected_musicbrainz_recording_id', sa.Text(), nullable=True))
        batch.add_column(sa.Column('expected_musicbrainz_release_id', sa.Text(), nullable=True))
        batch.create_check_constraint('ck_jobs_expected_musicbrainz_pair', _JOB_IDENTITY_PAIR_CONSTRAINT)
    op.create_index(
        'uq_active_musicbrainz_refresh_job',
        'jobs',
        ['source_id', 'kind'],
        unique=True,
        postgresql_where=sa.text("kind = 'musicbrainz_refresh' AND state IN ('queued', 'running')"),
        sqlite_where=sa.text("kind = 'musicbrainz_refresh' AND state IN ('queued', 'running')"),
    )


def downgrade() -> None:
    bind = op.get_bind()
    op.drop_index('uq_active_musicbrainz_refresh_job', table_name='jobs')
    with op.batch_alter_table('jobs') as batch:
        batch.drop_constraint('ck_jobs_expected_musicbrainz_pair', type_='check')
        batch.drop_column('expected_musicbrainz_release_id')
        batch.drop_column('expected_musicbrainz_recording_id')
    with op.batch_alter_table('library_records') as batch:
        batch.drop_constraint('ck_library_records_matched_identity', type_='check')
        batch.drop_constraint('ck_library_records_musicbrainz_pair', type_='check')
    if bind.dialect.name == 'postgresql':
        op.create_index(
            'uq_library_records_musicbrainz_recording_without_release',
            'library_records',
            ['musicbrainz_recording_id'],
            unique=True,
            postgresql_where=sa.text('musicbrainz_recording_id IS NOT NULL AND musicbrainz_release_id IS NULL'),
        )
