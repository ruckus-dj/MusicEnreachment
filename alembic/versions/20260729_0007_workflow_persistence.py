import sqlalchemy as sa

from alembic import op

revision = '20260729_0007'
down_revision = '20260728_0006'
branch_labels = None
depends_on = None


def upgrade() -> None:
    _ = op.create_table(
        'webhook_receipts',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('event_fingerprint', sa.String(64), nullable=False, unique=True),
        sa.Column('provider_name', sa.String(64), nullable=False),
        sa.Column('payload_json', sa.Text, nullable=False),
        sa.Column('received_at', sa.DateTime(timezone=True), nullable=False),
    )
    _ = op.create_table(
        'release_groups',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('title', sa.Text, nullable=False),
    )
    _ = op.create_table(
        'releases',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('release_group_id', sa.String(96), sa.ForeignKey('release_groups.id'), nullable=False),
        sa.Column('title', sa.Text, nullable=False),
    )
    _ = op.create_table(
        'tracks',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('release_id', sa.String(96), sa.ForeignKey('releases.id'), nullable=False),
        sa.Column('position', sa.Integer, nullable=False),
        sa.Column('title', sa.Text, nullable=False),
        sa.UniqueConstraint('release_id', 'position', name='uq_track_release_position'),
    )
    _ = op.create_table(
        'release_files',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('track_id', sa.String(96), sa.ForeignKey('tracks.id'), nullable=False),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id')),
        sa.Column('relative_path', sa.Text, nullable=False, unique=True),
        sa.Column('content_sha256', sa.String(64), nullable=False),
    )
    _ = op.create_table(
        'tag_layers',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('release_file_id', sa.String(96), sa.ForeignKey('release_files.id'), nullable=False),
        sa.Column('layer', sa.String(16), nullable=False),
        sa.Column('revision', sa.Integer, nullable=False),
        sa.Column('tags_json', sa.Text, nullable=False),
        sa.Column('recorded_at', sa.DateTime(timezone=True), nullable=False),
        sa.UniqueConstraint('release_file_id', 'layer', 'revision', name='uq_tag_layer_revision'),
    )
    _ = op.create_table(
        'jobs',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('kind', sa.String(64), nullable=False),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
    )
    _ = op.create_table(
        'job_attempts',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('job_id', sa.String(96), sa.ForeignKey('jobs.id'), nullable=False),
        sa.Column('attempt_number', sa.Integer, nullable=False),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('started_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('finished_at', sa.DateTime(timezone=True)),
        sa.UniqueConstraint('job_id', 'attempt_number', name='uq_job_attempt_number'),
    )
    _ = op.create_table(
        'publication_states',
        sa.Column('release_id', sa.String(96), sa.ForeignKey('releases.id'), primary_key=True),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )
    _ = op.create_table(
        'tombstones',
        sa.Column('release_file_id', sa.String(96), sa.ForeignKey('release_files.id'), primary_key=True),
        sa.Column('reason', sa.Text, nullable=False),
        sa.Column('recorded_at', sa.DateTime(timezone=True), nullable=False),
    )
    _ = op.create_table(
        'audit_records',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('release_id', sa.String(96), sa.ForeignKey('releases.id'), nullable=False),
        sa.Column('action', sa.String(64), nullable=False),
        sa.Column('actor', sa.String(128), nullable=False),
        sa.Column('details_json', sa.Text, nullable=False),
        sa.Column('recorded_at', sa.DateTime(timezone=True), nullable=False),
    )


def downgrade() -> None:
    op.drop_table('audit_records')
    op.drop_table('tombstones')
    op.drop_table('publication_states')
    op.drop_table('job_attempts')
    op.drop_table('jobs')
    op.drop_table('tag_layers')
    op.drop_table('release_files')
    op.drop_table('tracks')
    op.drop_table('releases')
    op.drop_table('release_groups')
    op.drop_table('webhook_receipts')
