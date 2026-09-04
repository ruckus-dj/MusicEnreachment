"""persist source replacement lineage

Revision ID: 20260904_0018
Revises: 20260819_0017
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260904_0018'
down_revision = '20260819_0017'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('source_records') as batch:
        batch.add_column(sa.Column('replaced_by_source_id', sa.Text(), nullable=True))
        batch.create_foreign_key(
            'fk_source_records_replaced_by_source_id',
            'source_records',
            ['replaced_by_source_id'],
            ['id'],
        )


def downgrade() -> None:
    with op.batch_alter_table('source_records') as batch:
        batch.drop_constraint('fk_source_records_replaced_by_source_id', type_='foreignkey')
        batch.drop_column('replaced_by_source_id')
