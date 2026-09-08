"""Add durable prepared publication checkpoint and cleanup progress."""

import sqlalchemy as sa

from alembic import op

revision = '20260908_0020'
down_revision = '20260904_0019'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('publication_attempts') as batch:
        batch.add_column(sa.Column('cleaned_at', sa.DateTime(timezone=True)))
        batch.drop_constraint('ck_publication_attempt_state', type_='check')
        batch.create_check_constraint(
            'ck_publication_attempt_state',
            "state IN ('reserved', 'staged', 'prepared', 'exposed', 'finalized', 'failed')",
        )


def downgrade() -> None:
    op.execute("UPDATE publication_attempts SET state = 'staged' WHERE state = 'prepared'")
    with op.batch_alter_table('publication_attempts') as batch:
        batch.drop_constraint('ck_publication_attempt_state', type_='check')
        batch.create_check_constraint(
            'ck_publication_attempt_state', "state IN ('reserved', 'staged', 'exposed', 'finalized', 'failed')"
        )
        batch.drop_column('cleaned_at')
