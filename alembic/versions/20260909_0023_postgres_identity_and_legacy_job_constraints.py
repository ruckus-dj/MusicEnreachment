"""preserve legacy jobs and recording-only identities

Revision ID: 20260909_0023
Revises: 20260909_0022
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260909_0023'
down_revision = '20260909_0022'
branch_labels = None
depends_on = None

_JOB_TARGET_CONSTRAINT = (
    '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
    + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
    + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END + '
    + 'CASE WHEN folder_path IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
    + "kind = 'reconciliation_scan'"
)


def upgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name != 'postgresql':
        return
    op.create_index(
        'uq_library_records_musicbrainz_recording_without_release',
        'library_records',
        ['musicbrainz_recording_id'],
        unique=True,
        postgresql_where=sa.text('musicbrainz_recording_id IS NOT NULL AND musicbrainz_release_id IS NULL'),
    )
    op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
    op.create_check_constraint(
        'ck_jobs_target_or_reconciliation',
        'jobs',
        _JOB_TARGET_CONSTRAINT,
        postgresql_not_valid=True,
    )


def downgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name != 'postgresql':
        return
    op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
    op.create_check_constraint(
        'ck_jobs_target_or_reconciliation',
        'jobs',
        _JOB_TARGET_CONSTRAINT,
        postgresql_not_valid=True,
    )
    op.drop_index('uq_library_records_musicbrainz_recording_without_release', table_name='library_records')
