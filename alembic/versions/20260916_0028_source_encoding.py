"""Preserve source text evidence and fence source-scoped processing revisions."""

import sqlalchemy as sa

from alembic import op

revision = '20260916_0028'
down_revision = '20260915_0026'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        'source_records', sa.Column('source_metadata_revision', sa.Integer(), nullable=False, server_default='1')
    )
    op.add_column('jobs', sa.Column('source_metadata_revision', sa.Integer(), nullable=True))
    op.add_column(
        'provider_candidate_runs',
        sa.Column('source_metadata_revision', sa.Integer(), nullable=False, server_default='1'),
    )
    for name in ('original_value', 'physical_id', 'declared_codec', 'applied_choice_json', 'extraction_version'):
        op.add_column('source_tag_observations', sa.Column(name, sa.Text(), nullable=True))
    for name in ('prepared_bytes', 'binary_evidence'):
        op.add_column('source_tag_observations', sa.Column(name, sa.LargeBinary(), nullable=True))
    op.add_column(
        'source_tag_observations', sa.Column('selected', sa.Boolean(), nullable=False, server_default=sa.true())
    )
    # Unicode is existing evidence; bytes remain NULL. No media I/O, no re-encoding.
    op.execute(sa.text('UPDATE source_tag_observations SET original_value = value'))
    op.execute(sa.text('UPDATE jobs SET source_metadata_revision = 1 WHERE source_id IS NOT NULL'))


def downgrade() -> None:
    op.drop_column('source_tag_observations', 'selected')
    for name in (
        'extraction_version',
        'binary_evidence',
        'prepared_bytes',
        'applied_choice_json',
        'declared_codec',
        'physical_id',
        'original_value',
    ):
        op.drop_column('source_tag_observations', name)
    op.drop_column('provider_candidate_runs', 'source_metadata_revision')
    op.drop_column('jobs', 'source_metadata_revision')
    op.drop_column('source_records', 'source_metadata_revision')
