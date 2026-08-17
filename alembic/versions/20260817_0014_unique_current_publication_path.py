"""prevent two current publications from owning one managed path

Revision ID: 20260817_0014
Revises: 20260816_0013
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260817_0014'
down_revision = '20260816_0013'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_index(
        'uq_current_library_publication_path',
        'library_publications',
        ['path'],
        unique=True,
        postgresql_where=sa.text("state = 'current'"),
        sqlite_where=sa.text("state = 'current'"),
    )


def downgrade() -> None:
    _ = op.drop_index('uq_current_library_publication_path', table_name='library_publications')
