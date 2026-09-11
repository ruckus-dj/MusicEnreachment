"""materialize current lyric state on library records

Revision ID: 20260911_0024
Revises: 20260909_0023
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260911_0024'
down_revision = '20260909_0023'
branch_labels = None
depends_on = None

_LYRICS_STATUS_CONSTRAINT = (
    "lyrics_status IN ('none', 'pending', 'synced', 'no_candidate', 'validation_rejected', 'error')"
)


def upgrade() -> None:
    with op.batch_alter_table('library_records') as batch:
        batch.add_column(sa.Column('lyrics_status', sa.Text(), nullable=False, server_default=sa.text("'none'")))
        batch.add_column(sa.Column('lyrics_path', sa.Text()))
        batch.add_column(
            sa.Column(
                'lyrics_publication_id',
                sa.Text(),
                sa.ForeignKey('library_publications.id', name='fk_library_records_lyrics_publication'),
            )
        )
        batch.add_column(sa.Column('lyrics_sha256', sa.Text()))
        batch.add_column(sa.Column('lyrics_updated_at', sa.DateTime(timezone=True)))
        batch.create_check_constraint('ck_library_records_lyrics_status', _LYRICS_STATUS_CONSTRAINT)
    op.create_index(
        'uq_active_lrclib_fetch_job',
        'jobs',
        ['library_record_id', 'kind'],
        unique=True,
        postgresql_where=sa.text("kind = 'lrclib_fetch' AND state IN ('queued', 'running')"),
        sqlite_where=sa.text("kind = 'lrclib_fetch' AND state IN ('queued', 'running')"),
    )


def downgrade() -> None:
    op.drop_index('uq_active_lrclib_fetch_job', table_name='jobs')
    with op.batch_alter_table('library_records') as batch:
        batch.drop_constraint('ck_library_records_lyrics_status', type_='check')
        batch.drop_constraint('fk_library_records_lyrics_publication', type_='foreignkey')
        batch.drop_column('lyrics_updated_at')
        batch.drop_column('lyrics_sha256')
        batch.drop_column('lyrics_publication_id')
        batch.drop_column('lyrics_path')
        batch.drop_column('lyrics_status')
