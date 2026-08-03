"""Persist immutable local Chromaprint evidence.

Revision ID: 20260728_0003
Revises: 20260728_0002
Create Date: 2026-07-28 00:00:00
"""

import sqlalchemy as sa

from alembic import op

revision = '20260728_0003'
down_revision = '20260728_0002'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'fingerprint_evidence',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('source_id', sa.String(length=64), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('state', sa.String(length=32), nullable=False),
        sa.Column('fingerprint', sa.Text()),
        sa.Column('duration_seconds', sa.Float()),
        sa.Column('tool_version', sa.String(length=64)),
        sa.Column('output_sha256', sa.String(length=64), nullable=False),
        sa.Column('tool_state', sa.String(length=32)),
        sa.Column('return_code', sa.Integer()),
        sa.Column('version_tool_state', sa.String(length=32)),
        sa.Column('version_return_code', sa.Integer()),
        sa.Column('version_output_sha256', sa.String(length=64)),
    )


def downgrade() -> None:
    op.drop_table('fingerprint_evidence')
