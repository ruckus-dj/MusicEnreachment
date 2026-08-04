"""Normalize publication paths after the storage-boundary migration.

Revision ID: 20260805_0013
Revises: 20260805_0012
"""

from alembic import op

revision = '20260805_0013'
down_revision = '20260805_0012'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute(
        'UPDATE publication_records '
        "SET published_path = replace(published_path, '/data/.publish/media/', '/data/media/') "
        "WHERE published_path LIKE '/data/.publish/media/%'"
    )


def downgrade() -> None:
    op.execute(
        'UPDATE publication_records '
        "SET published_path = replace(published_path, '/data/media/', '/data/.publish/media/') "
        "WHERE published_path LIKE '/data/media/%'"
    )
