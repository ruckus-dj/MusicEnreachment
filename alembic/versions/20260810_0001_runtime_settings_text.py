"""Allow structured runtime settings to exceed 255 characters."""

import sqlalchemy as sa

from alembic import op

revision = '20260810_0001'
down_revision = '20260807_0001'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('runtime_settings', recreate='always') as batch:
        batch.alter_column('value', existing_type=sa.String(length=255), type_=sa.Text(), nullable=False)


def downgrade() -> None:
    with op.batch_alter_table('runtime_settings', recreate='always') as batch:
        batch.alter_column('value', existing_type=sa.Text(), type_=sa.String(length=255), nullable=False)
