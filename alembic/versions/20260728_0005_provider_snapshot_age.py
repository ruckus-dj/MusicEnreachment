"""Persist provider snapshot age for stale-cache review evidence.

Revision ID: 20260728_0005
Revises: 20260728_0004
Create Date: 2026-07-28 00:00:00
"""

import sqlalchemy as sa

from alembic import op

revision = '20260728_0005'
down_revision = '20260728_0004'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column('provider_snapshots', sa.Column('age_seconds', sa.Integer(), nullable=True))


def downgrade() -> None:
    op.drop_column('provider_snapshots', 'age_seconds')
