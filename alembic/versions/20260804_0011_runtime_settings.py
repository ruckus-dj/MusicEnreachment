"""Persist operator-adjustable runtime settings.

Revision ID: 20260804_0011
Revises: 20260729_0010
"""

import sqlalchemy as sa

from alembic import op

revision = '20260804_0011'
down_revision = '20260729_0010'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'runtime_settings',
        sa.Column('key', sa.String(length=64), primary_key=True),
        sa.Column('value', sa.String(length=255), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )


def downgrade() -> None:
    op.drop_table('runtime_settings')
