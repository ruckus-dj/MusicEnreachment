"""Persist resumable storage copy, switch and cleanup progress."""

import sqlalchemy as sa

from alembic import op

revision = '20260909_0021'
down_revision = '20260908_0020'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column('storage_config', sa.Column('migration_json', sa.Text()))


def downgrade() -> None:
    bind = op.get_bind()
    if bind.execute(sa.text("SELECT count(*) FROM storage_config WHERE state = 'migrating'")).scalar_one():
        raise RuntimeError('finish storage migration before downgrading')
    op.drop_column('storage_config', 'migration_json')
