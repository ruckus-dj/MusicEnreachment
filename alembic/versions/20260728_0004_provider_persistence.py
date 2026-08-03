"""Persist sanitized global provider snapshots and seeded provider schedules.

Revision ID: 20260728_0004
Revises: 20260728_0003
Create Date: 2026-07-28 00:00:00
"""

from datetime import UTC, datetime

import sqlalchemy as sa

from alembic import op

revision = '20260728_0004'
down_revision = '20260728_0003'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'provider_snapshots',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('provider_name', sa.String(length=64), nullable=False),
        sa.Column('request_hash', sa.String(length=64), nullable=False),
        sa.Column('request_descriptor', sa.Text(), nullable=False),
        sa.Column('response_sha256', sa.String(length=64), nullable=False),
        sa.Column('response_body', sa.LargeBinary(), nullable=True),
        sa.Column('captured_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('outcome', sa.String(length=32), nullable=False),
        sa.Column('state', sa.String(length=32), nullable=False),
        sa.Column('http_status', sa.Integer()),
    )
    op.create_index(
        'ix_provider_snapshots_lookup', 'provider_snapshots', ['provider_name', 'request_hash', 'captured_at']
    )
    _ = op.create_table(
        'provider_schedules',
        sa.Column('provider_name', sa.String(length=64), primary_key=True),
        sa.Column('next_start_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('lease_until', sa.DateTime(timezone=True)),
        sa.Column('lease_token', sa.String(length=64)),
    )
    schedule = sa.table(
        'provider_schedules',
        sa.column('provider_name', sa.String(length=64)),
        sa.column('next_start_at', sa.DateTime(timezone=True)),
    )
    op.bulk_insert(
        schedule,
        [
            {'provider_name': 'musicbrainz', 'next_start_at': datetime(1970, 1, 1, tzinfo=UTC)},
            {'provider_name': 'acoustid', 'next_start_at': datetime(1970, 1, 1, tzinfo=UTC)},
        ],
    )


def downgrade() -> None:
    op.drop_table('provider_schedules')
    op.drop_index('ix_provider_snapshots_lookup', table_name='provider_snapshots')
    op.drop_table('provider_snapshots')
