"""Add device-neutral source identity and current locations."""

from __future__ import annotations

from typing import cast

import sqlalchemy as sa

from alembic import op

revision = '20260925_0034'
down_revision = '20260924_0033'
branch_labels = None
depends_on = None

_IDENTITY_BACKFILL = (
    "UPDATE source_records SET identity_key = CAST(inode AS TEXT) || ':' || "
    "CAST(size_bytes AS TEXT) || ':' || CAST(mtime_ns AS TEXT)"
)
_LOCATION_BACKFILL = (
    'INSERT INTO source_locations (source_id, source_root_id, path) '
    'SELECT id, source_root_id, source_path FROM ('
    'SELECT id, source_root_id, source_path, '
    'ROW_NUMBER() OVER ('
    'PARTITION BY source_root_id, source_path '
    "ORDER BY CASE WHEN intake_state = 'present' THEN 0 ELSE 1 END, device DESC, id"
    ') AS position FROM source_records '
    "WHERE intake_state NOT IN ('replaced', 'disappeared')"
    ') ranked WHERE position = 1'
)


def upgrade() -> None:
    op.add_column(
        'source_records',
        sa.Column('identity_key', sa.Text(), nullable=False, server_default='legacy'),
    )
    op.execute(sa.text(_IDENTITY_BACKFILL))
    op.create_index('ix_source_records_identity_key', 'source_records', ['identity_key'])
    _ = op.create_table(
        'source_locations',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('source_id', sa.Text(), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('source_root_id', sa.Text(), sa.ForeignKey('source_roots.id'), nullable=False),
        sa.Column('path', sa.Text(), nullable=False),
        sa.UniqueConstraint('source_root_id', 'path', name='uq_source_locations_root_path'),
    )
    op.execute(sa.text(_LOCATION_BACKFILL))
    op.create_index('ix_source_locations_source_id', 'source_locations', ['source_id'])
    with op.batch_alter_table('library_publications') as batch:
        batch.alter_column('source_id', existing_type=sa.Text(), nullable=True)


def downgrade() -> None:
    bind = op.get_bind()
    nullable_publication_count = cast(
        int,
        bind.scalar(sa.text('SELECT COUNT(*) FROM library_publications WHERE source_id IS NULL')),
    )
    if nullable_publication_count > 0:
        raise RuntimeError('cannot downgrade source locations while publications without source provenance exist')
    with op.batch_alter_table('library_publications') as batch:
        batch.alter_column('source_id', existing_type=sa.Text(), nullable=False)
    op.drop_index('ix_source_locations_source_id', table_name='source_locations')
    op.drop_table('source_locations')
    op.drop_index('ix_source_records_identity_key', table_name='source_records')
    op.drop_column('source_records', 'identity_key')
