"""Remove the obsolete provider-specific worker pool.

Revision ID: 20260915_0026
Revises: 20260914_0025
"""

import json

import sqlalchemy as sa

from alembic import op

revision = '20260915_0026'
down_revision = '20260914_0027'
branch_labels = None
depends_on = None


def upgrade() -> None:
    connection = op.get_bind()
    settings = connection.execute(
        sa.text("SELECT value FROM runtime_settings WHERE key = 'processing.worker_pools'")
    ).scalar_one_or_none()
    if settings is None:
        return
    pools = json.loads(settings)
    if not isinstance(pools, dict) or 'lidarr_intake' not in pools:
        return
    del pools['lidarr_intake']
    connection.execute(
        sa.text("UPDATE runtime_settings SET value = :value WHERE key = 'processing.worker_pools'"),
        {'value': json.dumps(pools, sort_keys=True)},
    )


def downgrade() -> None:
    pass
