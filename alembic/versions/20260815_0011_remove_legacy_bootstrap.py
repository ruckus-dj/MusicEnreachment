"""remove legacy bootstrap source root

Revision ID: 20260815_0011
Revises: 20260814_0010
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260815_0011'
down_revision = '20260814_0010'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name != 'postgresql':
        return
    statement = "UPDATE source_records SET source_root_id = 'historical-unmanaged' WHERE source_root_id = 'legacy'"
    bind.execute(sa.text(statement))
    bind.execute(sa.text("DELETE FROM source_roots WHERE id = 'legacy'"))


def downgrade() -> None:
    return None
