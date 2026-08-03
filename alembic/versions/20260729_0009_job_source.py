"""Associate durable processing jobs with immutable sources.

Revision ID: 20260729_0009
Revises: 20260729_0008
"""

import sqlalchemy as sa

from alembic import op

revision = '20260729_0009'
down_revision = '20260729_0008'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('jobs') as batch:
        batch.add_column(
            sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id', name='fk_jobs_source_id'))
        )
        batch.create_index('ix_jobs_source_id', ['source_id'])


def downgrade() -> None:
    with op.batch_alter_table('jobs') as batch:
        batch.drop_index('ix_jobs_source_id')
        batch.drop_column('source_id')
