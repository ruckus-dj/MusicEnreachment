"""Distinguish protected manual source decisions from automatic recovery."""

import sqlalchemy as sa

from alembic import op

revision = '20260916_0029'
down_revision = '20260916_0028'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column('source_tag_observations', sa.Column('decision_origin', sa.Text(), nullable=True))
    op.execute(
        sa.text("UPDATE source_tag_observations SET decision_origin = 'manual' WHERE applied_choice_json IS NOT NULL")
    )


def downgrade() -> None:
    op.drop_column('source_tag_observations', 'decision_origin')
