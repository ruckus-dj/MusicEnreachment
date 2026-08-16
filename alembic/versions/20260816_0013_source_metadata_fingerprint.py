"""store source filesystem modification time

Revision ID: 20260816_0013
Revises: 20260816_0012
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260816_0013'
down_revision = '20260816_0012'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.add_column(
        'source_records',
        sa.Column('mtime_ns', sa.BigInteger(), nullable=False, server_default='0'),
    )


def downgrade() -> None:
    _ = op.drop_column('source_records', 'mtime_ns')
