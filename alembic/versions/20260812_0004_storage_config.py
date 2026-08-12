"""persist configurable output storage

Revision ID: 20260812_0004
Revises: 20260811_0003
"""

from __future__ import annotations

import os

import sqlalchemy as sa

from alembic import op

revision = '20260812_0004'
down_revision = '20260811_0003'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'storage_config',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('output_root', sa.Text(), nullable=False),
        sa.Column('state', sa.String(16), nullable=False),
        sa.Column('generation', sa.Integer(), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )
    if op.get_bind().dialect.name == 'postgresql':
        output_root = os.environ.get('MUSIC_INGEST_MEDIA_ROOT')
        if output_root is None:
            raise RuntimeError('MUSIC_INGEST_MEDIA_ROOT is required')
        op.execute(
            sa.text(
                'INSERT INTO storage_config (id, output_root, state, generation, updated_at) '
                "VALUES (1, :output_root, 'ready', 1, CURRENT_TIMESTAMP)"
            ).bindparams(output_root=output_root)
        )


def downgrade() -> None:
    op.drop_table('storage_config')
