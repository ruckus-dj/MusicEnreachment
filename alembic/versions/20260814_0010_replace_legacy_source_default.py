"""replace legacy source-root default

Revision ID: 20260814_0010
Revises: 20260813_0009
"""

from __future__ import annotations

from alembic import op

revision = '20260814_0010'
down_revision = '20260813_0009'
branch_labels = None
depends_on = None


def upgrade() -> None:
    if op.get_bind().dialect.name == 'postgresql':
        op.alter_column('source_records', 'source_root_id', server_default='historical-unmanaged')


def downgrade() -> None:
    if op.get_bind().dialect.name == 'postgresql':
        op.alter_column('source_records', 'source_root_id', server_default='legacy')
