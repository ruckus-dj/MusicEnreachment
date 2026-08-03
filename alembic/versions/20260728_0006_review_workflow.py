"""Add append-only review decisions and local release identities."""

import sqlalchemy as sa

from alembic import op

revision = '20260728_0006'
down_revision = '20260728_0005'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'review_releases',
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), primary_key=True),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('original_json', sa.Text, nullable=False),
        sa.Column('proposed_json', sa.Text, nullable=False),
        sa.Column('artist_id', sa.String(96), nullable=False),
        sa.Column('release_id', sa.String(96), nullable=False),
        sa.Column('track_id', sa.String(96), nullable=False),
        sa.Column('musicbrainz_id', sa.String(36)),
    )
    op.create_table(
        'review_audits',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('action', sa.String(32), nullable=False),
        sa.Column('before_json', sa.Text, nullable=False),
        sa.Column('after_json', sa.Text, nullable=False),
        sa.Column('actor', sa.String(128), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.create_table(
        'publish_snapshots',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('release_json', sa.Text, nullable=False),
        sa.Column('captured_at', sa.DateTime(timezone=True), nullable=False),
    )


def downgrade() -> None:
    op.drop_table('publish_snapshots')
    op.drop_table('review_audits')
    op.drop_table('review_releases')
