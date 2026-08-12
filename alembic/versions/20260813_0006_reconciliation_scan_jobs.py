from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260813_0006'
down_revision = '20260812_0005'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    op.add_column('jobs', sa.Column('result_json', sa.Text(), nullable=True))
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_exactly_one_target', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_target_or_reconciliation',
            'jobs',
            "((source_id IS NOT NULL) != (library_record_id IS NOT NULL)) OR kind = 'reconciliation_scan'",
        )
    op.create_index(
        'uq_active_reconciliation_scan_job',
        'jobs',
        ['kind'],
        unique=True,
        postgresql_where=sa.text("kind = 'reconciliation_scan' AND state IN ('queued', 'running')"),
        sqlite_where=sa.text("kind = 'reconciliation_scan' AND state IN ('queued', 'running')"),
    )


def downgrade() -> None:
    bind = op.get_bind()
    op.drop_index('uq_active_reconciliation_scan_job', table_name='jobs')
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_target_or_reconciliation', 'jobs', type_='check')
        op.create_check_constraint(
            'ck_jobs_exactly_one_target', 'jobs', '(source_id IS NOT NULL) != (library_record_id IS NOT NULL)'
        )
    op.drop_column('jobs', 'result_json')
