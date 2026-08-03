"""Persist immutable source provenance and evidence.

Revision ID: 20260728_0002
Revises: 20260727_0001
Create Date: 2026-07-28 00:00:00
"""

import sqlalchemy as sa

from alembic import op

revision = '20260728_0002'
down_revision = '20260727_0001'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'source_records',
        sa.Column('id', sa.String(length=64), primary_key=True),
        sa.Column('source_path', sa.Text(), nullable=False),
        sa.Column('device', sa.BigInteger(), nullable=False),
        sa.Column('inode', sa.BigInteger(), nullable=False),
        sa.Column('size_bytes', sa.BigInteger(), nullable=False),
        sa.Column('sha256', sa.String(length=64), nullable=False),
        sa.Column('duration_seconds', sa.Integer()),
        sa.Column('origin', sa.String(length=16), nullable=False),
        sa.Column('intake_state', sa.String(length=32), nullable=False),
        sa.UniqueConstraint('device', 'inode', 'sha256', name='uq_source_file_identity'),
    )
    for table_name, columns, constraint in _evidence_tables():
        _ = op.create_table(table_name, *columns, constraint)


def downgrade() -> None:
    for table_name, _, _ in reversed(_evidence_tables()):
        op.drop_table(table_name)
    op.drop_table('source_records')


def _evidence_tables():
    def source_id():
        return sa.Column('source_id', sa.String(length=64), sa.ForeignKey('source_records.id'), nullable=False)

    return (
        (
            'source_tag_observations',
            (
                sa.Column('id', sa.Integer(), primary_key=True),
                source_id(),
                sa.Column('format_name', sa.String(length=32), nullable=False),
                sa.Column('tag_name', sa.String(length=128), nullable=False),
                sa.Column('value', sa.Text(), nullable=False),
            ),
            sa.UniqueConstraint('source_id', 'format_name', 'tag_name', 'value', name='uq_source_tag_value'),
        ),
        (
            'artwork_hash_observations',
            (
                sa.Column('id', sa.Integer(), primary_key=True),
                source_id(),
                sa.Column('sha256', sa.String(length=64), nullable=False),
            ),
            sa.UniqueConstraint('source_id', 'sha256', name='uq_artwork_hash'),
        ),
        (
            'provider_attempts',
            (
                sa.Column('id', sa.Integer(), primary_key=True),
                source_id(),
                sa.Column('provider_name', sa.String(length=64), nullable=False),
                sa.Column('outcome', sa.String(length=32), nullable=False),
                sa.Column('snapshot_sha256', sa.String(length=64), nullable=False),
                sa.Column('snapshot', sa.Text(), nullable=False),
            ),
            sa.UniqueConstraint('source_id', 'provider_name', 'snapshot_sha256', name='uq_provider_snapshot'),
        ),
        (
            'candidate_evidence',
            (
                sa.Column('id', sa.Integer(), primary_key=True),
                source_id(),
                sa.Column('candidate_key', sa.String(length=255), nullable=False),
                sa.Column('evidence', sa.Text(), nullable=False),
            ),
            sa.UniqueConstraint('source_id', 'candidate_key', name='uq_candidate_evidence'),
        ),
        (
            'review_decisions',
            (
                sa.Column('id', sa.Integer(), primary_key=True),
                source_id(),
                sa.Column('state', sa.String(length=32), nullable=False),
                sa.Column('rationale', sa.Text(), nullable=False),
            ),
            sa.UniqueConstraint('source_id', 'state', 'rationale', name='uq_review_decision'),
        ),
        (
            'publication_records',
            (
                sa.Column('source_id', sa.String(length=64), sa.ForeignKey('source_records.id'), primary_key=True),
                sa.Column('publication_state', sa.String(length=32), nullable=False),
                sa.Column('published_path', sa.Text()),
            ),
            sa.UniqueConstraint('source_id', name='uq_publication_source'),
        ),
    )
