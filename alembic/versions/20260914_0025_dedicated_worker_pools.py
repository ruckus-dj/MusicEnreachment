"""Replace global worker capacity with independent dedicated pools.

Revision ID: 20260914_0025
Revises: 20260911_0024
"""

import json

import sqlalchemy as sa

from alembic import op

revision = '20260914_0025'
down_revision = '20260911_0024'
branch_labels = None
depends_on = None

_DEFAULTS = {
    'filesystem_scan': 4,
    'acoustid_analysis': 1,
    'musicbrainz_analysis': 4,
    'candidate_selection': 4,
    'folder_release_selection': 2,
    'final_publish': 4,
    'selection_refresh': 4,
    'lrclib_fetch': 1,
    'artwork_enrichment': 2,
    'reconciliation_scan': 1,
    'lidarr_intake': 1,
}


def upgrade() -> None:
    # Keep the legacy scalar for a lossless rollback. The new application ignores it;
    # the old application ignores the new pool setting.
    connection = op.get_bind()
    connection.execute(
        sa.text(
            'INSERT INTO runtime_settings (key, value, updated_at) '
            "SELECT 'processing.worker_pools', :value, CURRENT_TIMESTAMP "
            "WHERE NOT EXISTS (SELECT 1 FROM runtime_settings WHERE key = 'processing.worker_pools')"
        ),
        {'value': json.dumps(_DEFAULTS)},
    )


def downgrade() -> None:
    # The legacy scalar was deliberately retained during upgrade for compatibility.
    op.execute("DELETE FROM runtime_settings WHERE key = 'processing.worker_pools'")
