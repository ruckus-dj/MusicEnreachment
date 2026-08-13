from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260813_0009'
down_revision = '20260813_0008'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'decoder_evidence',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('source_id', sa.Text(), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('decoder_command', sa.Text(), nullable=False),
        sa.Column('tool_state', sa.Text(), nullable=False),
        sa.Column('return_code', sa.Integer()),
        sa.Column('output_sha256', sa.Text(), nullable=False),
        sa.Column('checked_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.create_index('ix_decoder_evidence_cache', 'decoder_evidence', ['source_id', 'decoder_command', 'tool_state'])


def downgrade() -> None:
    op.drop_index('ix_decoder_evidence_cache', table_name='decoder_evidence')
    op.drop_table('decoder_evidence')
