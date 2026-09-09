"""add folder release selection jobs

Revision ID: 20260818_0016
Revises: 20260818_0015
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260818_0016'
down_revision = '20260818_0015'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    op.add_column('jobs', sa.Column('folder_path', sa.Text(), nullable=True))
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN folder_path IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
            + "kind = 'reconciliation_scan'",
            postgresql_not_valid=True,
        )
        op.create_index(
            'uq_active_folder_release_selection_job',
            'jobs',
            ['folder_path', 'kind'],
            unique=True,
            postgresql_where=sa.text("kind = 'folder_release_selection' AND state IN ('queued', 'running')"),
        )
    else:
        op.create_index(
            'uq_active_folder_release_selection_job',
            'jobs',
            ['folder_path', 'kind'],
            unique=True,
            sqlite_where=sa.text("kind = 'folder_release_selection' AND state IN ('queued', 'running')"),
        )


def downgrade() -> None:
    bind = op.get_bind()
    op.drop_index('uq_active_folder_release_selection_job', table_name='jobs')
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
            + "kind = 'reconciliation_scan'",
            postgresql_not_valid=True,
        )
    op.drop_column('jobs', 'folder_path')
