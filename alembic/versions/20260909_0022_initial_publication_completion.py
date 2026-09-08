"""Preserve initial-ingest review/provider state across publication recovery."""

import sqlalchemy as sa

from alembic import op

revision = '20260909_0022'
down_revision = '20260909_0021'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        'publication_attempts', sa.Column('completion_state', sa.Text(), nullable=False, server_default='complete')
    )


def downgrade() -> None:
    op.drop_column('publication_attempts', 'completion_state')
