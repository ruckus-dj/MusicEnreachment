"""add independent release artwork state and jobs

Revision ID: 20260818_0015
Revises: 20260817_0014
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260818_0015'
down_revision = '20260817_0014'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    _ = op.create_table(
        'release_artwork',
        sa.Column('release_mbid', sa.Text(), primary_key=True),
        sa.Column('path', sa.Text(), nullable=True),
        sa.Column('format_name', sa.Text(), nullable=True),
        sa.Column('provider', sa.Text(), nullable=False),
        sa.Column('state', sa.Text(), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.add_column('jobs', sa.Column('release_mbid', sa.Text(), nullable=True))
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
            + "kind = 'reconciliation_scan'",
        )


def downgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            "((source_id IS NOT NULL) != (library_record_id IS NOT NULL)) OR kind = 'reconciliation_scan'",
        )
    op.drop_column('jobs', 'release_mbid')
    op.drop_table('release_artwork')
