"""Create the source-agnostic stable library schema.

Revision ID: 20260806_0001
Revises:
"""

import sqlalchemy as sa

from alembic import op

revision = '20260806_0001'
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    from music_ingest.persistence.models import Base

    Base.metadata.create_all(bind=bind)
    op.execute(
        sa.text(
            'INSERT INTO provider_schedules (provider_name, next_start_at) '
            + "VALUES ('musicbrainz', CURRENT_TIMESTAMP), ('acoustid', CURRENT_TIMESTAMP) "
            + 'ON CONFLICT (provider_name) DO NOTHING'
        )
    )


def downgrade() -> None:
    bind = op.get_bind()
    from music_ingest.persistence.models import Base

    Base.metadata.drop_all(bind=bind)
