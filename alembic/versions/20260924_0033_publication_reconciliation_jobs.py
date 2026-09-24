"""Add publication-directory reconciliation jobs."""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260924_0033'
down_revision = '20260924_0032'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN folder_path IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
            + "kind IN ('reconciliation_scan', 'publication_reconciliation')",
            postgresql_not_valid=True,
        )
    op.create_index(
        'uq_active_publication_reconciliation_job',
        'jobs',
        ['kind'],
        unique=True,
        postgresql_where=sa.text("kind = 'publication_reconciliation' AND state IN ('queued', 'running')"),
        sqlite_where=sa.text("kind = 'publication_reconciliation' AND state IN ('queued', 'running')"),
    )


def downgrade() -> None:
    bind = op.get_bind()
    op.drop_index('uq_active_publication_reconciliation_job', table_name='jobs')
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
