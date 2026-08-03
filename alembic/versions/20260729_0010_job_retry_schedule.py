"""Persist durable processing retry schedules.

Revision ID: 20260729_0010
Revises: 20260729_0009
"""

import sqlalchemy as sa

from alembic import op

revision = '20260729_0010'
down_revision = '20260729_0009'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('jobs') as batch:
        batch.add_column(sa.Column('next_attempt_at', sa.DateTime(timezone=True)))
        batch.create_index('ix_jobs_next_attempt_at', ['next_attempt_at'])


def downgrade() -> None:
    with op.batch_alter_table('jobs') as batch:
        batch.drop_index('ix_jobs_next_attempt_at')
        batch.drop_column('next_attempt_at')
