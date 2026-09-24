"""Add the active-source index used by library status."""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260924_0032'
down_revision = '20260923_0031'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_index(
        'ix_source_records_present_library_record_id',
        'source_records',
        ['library_record_id'],
        postgresql_where=sa.text("intake_state = 'present'"),
        sqlite_where=sa.text("intake_state = 'present'"),
    )


def downgrade() -> None:
    op.drop_index('ix_source_records_present_library_record_id', table_name='source_records')
