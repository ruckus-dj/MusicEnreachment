import sqlalchemy as sa

from alembic import op

revision = '20260810_0002'
down_revision = '20260810_0001'
branch_labels = None
depends_on = None


def upgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    if 'genre_catalog' not in inspector.get_table_names():
        op.create_table(
            'genre_catalog',
            sa.Column('musicbrainz_id', sa.String(length=64), nullable=False),
            sa.Column('source_name', sa.String(length=255), nullable=False),
            sa.Column('display_name', sa.String(length=255), nullable=False),
            sa.Column('normalized_key', sa.String(length=255), nullable=False),
            sa.Column('synced_at', sa.DateTime(timezone=True), nullable=False),
            sa.PrimaryKeyConstraint('musicbrainz_id'),
            sa.UniqueConstraint('source_name'),
        )
    if 'ix_genre_catalog_normalized_key' not in {index['name'] for index in inspector.get_indexes('genre_catalog')}:
        op.create_index('ix_genre_catalog_normalized_key', 'genre_catalog', ['normalized_key'])


def downgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    if 'genre_catalog' in inspector.get_table_names():
        if 'ix_genre_catalog_normalized_key' in {index['name'] for index in inspector.get_indexes('genre_catalog')}:
            op.drop_index('ix_genre_catalog_normalized_key', table_name='genre_catalog')
        op.drop_table('genre_catalog')
