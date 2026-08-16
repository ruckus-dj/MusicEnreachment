"""add durable Unsorted filename counter

Revision ID: 20260816_0012
Revises: 20260815_0011
"""

from __future__ import annotations

import os
import re
from pathlib import Path

import sqlalchemy as sa

from alembic import op

revision = '20260816_0012'
down_revision = '20260815_0011'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'unsorted_filename_counters',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('next_number', sa.Integer(), nullable=False),
    )
    initial_number = 0
    media_root = os.environ.get('MUSIC_INGEST_MEDIA_ROOT')
    unsorted_directory = None if media_root is None else Path(media_root) / 'Unsorted'
    if unsorted_directory is not None and unsorted_directory.is_dir():
        initial_number = max(
            (
                int(match.group(1))
                for path in unsorted_directory.glob('Track *')
                if (match := re.fullmatch(r'Track (\d{2})\.[^.]+', path.name)) is not None
            ),
            default=0,
        )
    op.execute(
        sa.text('INSERT INTO unsorted_filename_counters (id, next_number) VALUES (1, :initial_number)').bindparams(
            initial_number=initial_number
        )
    )


def downgrade() -> None:
    op.drop_table('unsorted_filename_counters')
