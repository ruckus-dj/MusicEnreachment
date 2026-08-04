"""Persist processing failure reasons in the database.

Revision ID: 20260805_0012
Revises: 20260804_0011
"""

import sqlalchemy as sa

from alembic import op

revision = '20260805_0012'
down_revision = '20260804_0011'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column('jobs', sa.Column('failure_reason', sa.Text(), nullable=True))


def downgrade() -> None:
    op.drop_column('jobs', 'failure_reason')
