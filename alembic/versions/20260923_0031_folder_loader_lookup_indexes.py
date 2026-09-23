"""Add folder loader lookup indexes."""

from __future__ import annotations

from alembic import op

revision = '20260923_0031'
down_revision = '20260923_0030'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_index('ix_provider_candidate_runs_source_id', 'provider_candidate_runs', ['source_id'])
    op.create_index('ix_candidate_evidence_source_id', 'candidate_evidence', ['source_id'])
    op.create_index('ix_candidate_evidence_run_id', 'candidate_evidence', ['run_id'])
    op.create_index('ix_source_tag_observations_source_id', 'source_tag_observations', ['source_id'])


def downgrade() -> None:
    op.drop_index('ix_source_tag_observations_source_id', table_name='source_tag_observations')
    op.drop_index('ix_candidate_evidence_run_id', table_name='candidate_evidence')
    op.drop_index('ix_candidate_evidence_source_id', table_name='candidate_evidence')
    op.drop_index('ix_provider_candidate_runs_source_id', table_name='provider_candidate_runs')
