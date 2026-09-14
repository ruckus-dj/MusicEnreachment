"""Index live publication intents for record-serialized idempotent reservation.

Revision ID: 20260914_0026
Revises: 20260914_0025

Existing journals are deliberately retained: even duplicate exposed attempts can
own backups or renamed output and must go through staged recovery, not SQL deletion.
Uniqueness across current publications and pending journals is enforced by the
canonical record transaction lock, shared by reservation and finalization.
"""

import sqlalchemy as sa

from alembic import op

revision = '20260914_0026'
down_revision = '20260914_0025'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_index(
        'ix_pending_publication_intent',
        'publication_attempts',
        ['library_record_id', 'source_id', 'metadata_revision_id'],
        postgresql_where=sa.text("state IN ('reserved', 'staged', 'prepared', 'exposed')"),
        sqlite_where=sa.text("state IN ('reserved', 'staged', 'prepared', 'exposed')"),
    )


def downgrade() -> None:
    op.drop_index('ix_pending_publication_intent', table_name='publication_attempts')
