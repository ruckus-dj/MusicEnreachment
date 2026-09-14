"""Keep reusable lyric evidence separate from the current publication binding."""

import sqlalchemy as sa

from alembic import op

revision = '20260914_0027'
down_revision = '20260914_0026'
branch_labels = None
depends_on = None


def upgrade() -> None:
    # No backfill: historical URL hashes do not prove identity, duration or settings.
    op.add_column('library_records', sa.Column('lyrics_evidence_json', sa.Text(), nullable=True))


def downgrade() -> None:
    op.drop_column('library_records', 'lyrics_evidence_json')
