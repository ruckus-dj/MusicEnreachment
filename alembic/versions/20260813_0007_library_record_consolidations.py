from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260813_0007'
down_revision = '20260813_0006'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'library_record_consolidations',
        sa.Column('retired_library_record_id', sa.Text(), sa.ForeignKey('library_records.id'), primary_key=True),
        sa.Column('canonical_library_record_id', sa.Text(), sa.ForeignKey('library_records.id'), nullable=False),
        sa.Column('sha256', sa.Text(), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.create_index('ix_source_records_sha256', 'source_records', ['sha256'])
    bind = op.get_bind()
    _ = bind.execute(
        sa.text(
            ' '.join(
                (
                    'INSERT INTO library_record_consolidations',
                    'SELECT source_records.library_record_id, duplicates.canonical_library_record_id,',
                    'source_records.sha256, CURRENT_TIMESTAMP',
                    'FROM source_records JOIN (',
                    'SELECT sha256, MIN(library_record_id) AS canonical_library_record_id',
                    'FROM source_records WHERE library_record_id IS NOT NULL GROUP BY sha256',
                    'HAVING COUNT(DISTINCT library_record_id) > 1',
                    ') AS duplicates ON duplicates.sha256 = source_records.sha256',
                    'WHERE source_records.library_record_id != duplicates.canonical_library_record_id',
                    'AND NOT EXISTS (SELECT 1 FROM source_records AS sibling',
                    'WHERE sibling.library_record_id = source_records.library_record_id',
                    'AND sibling.sha256 != source_records.sha256)',
                )
            )
        )
    )
    _ = bind.execute(
        sa.text(
            ' '.join(
                (
                    'INSERT INTO source_recording_assignments',
                    '(source_id, library_record_id, state, actor, rationale, evidence_json, created_at)',
                    'SELECT source_records.id, library_record_consolidations.canonical_library_record_id,',
                    "'content_sha256_consolidated', 'migration',",
                    "'exact source SHA-256 matched another observation', '{}', CURRENT_TIMESTAMP",
                    'FROM source_records JOIN library_record_consolidations',
                    'ON library_record_consolidations.retired_library_record_id = source_records.library_record_id',
                    'AND library_record_consolidations.sha256 = source_records.sha256',
                )
            )
        )
    )
    _ = bind.execute(
        sa.text(
            ' '.join(
                (
                    'UPDATE source_records SET library_record_id = (',
                    'SELECT canonical_library_record_id FROM library_record_consolidations',
                    'WHERE library_record_consolidations.retired_library_record_id = source_records.library_record_id',
                    'AND library_record_consolidations.sha256 = source_records.sha256',
                    ') WHERE library_record_id IN (',
                    'SELECT retired_library_record_id FROM library_record_consolidations',
                    ') AND sha256 IN (SELECT sha256 FROM library_record_consolidations)',
                )
            )
        )
    )


def downgrade() -> None:
    op.drop_index('ix_source_records_sha256', table_name='source_records')
    op.drop_table('library_record_consolidations')
