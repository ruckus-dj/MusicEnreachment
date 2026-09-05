"""add catalog query indexes

Revision ID: 20260904_0019
Revises: 20260904_0018
"""

from __future__ import annotations

from alembic import op

revision = '20260904_0019'
down_revision = '20260904_0018'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_index('ix_source_records_library_record_id', 'source_records', ['library_record_id'])
    op.create_index(
        'ix_source_tag_observations_tag_value_source',
        'source_tag_observations',
        ['tag_name', 'value', 'source_id'],
    )
    op.create_index(
        'ix_library_records_release_publication',
        'library_records',
        ['musicbrainz_release_id', 'publication_state'],
    )


def downgrade() -> None:
    op.drop_index('ix_library_records_release_publication', table_name='library_records')
    op.drop_index('ix_source_tag_observations_tag_value_source', table_name='source_tag_observations')
    op.drop_index('ix_source_records_library_record_id', table_name='source_records')
