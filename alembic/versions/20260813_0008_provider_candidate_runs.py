from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260813_0008'
down_revision = '20260813_0007'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'provider_candidate_runs',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('source_id', sa.Text(), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('provider_name', sa.Text(), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
    )
    with op.batch_alter_table('candidate_evidence') as batch:
        batch.add_column(sa.Column('run_id', sa.Integer()))
        batch.create_foreign_key('fk_candidate_evidence_run_id', 'provider_candidate_runs', ['run_id'], ['id'])
    op.add_column(
        'provider_attempts',
        sa.Column(
            'created_at', sa.DateTime(timezone=True), nullable=False, server_default=sa.text('CURRENT_TIMESTAMP')
        ),
    )


def downgrade() -> None:
    op.drop_column('provider_attempts', 'created_at')
    with op.batch_alter_table('candidate_evidence') as batch:
        batch.drop_constraint('fk_candidate_evidence_run_id', type_='foreignkey')
        batch.drop_column('run_id')
    op.drop_table('provider_candidate_runs')
