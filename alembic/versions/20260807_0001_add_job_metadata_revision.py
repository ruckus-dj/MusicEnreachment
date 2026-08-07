import sqlalchemy as sa

from alembic import op

revision = '20260807_0001'
down_revision = '20260806_0001'
branch_labels = None
depends_on = None


def upgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    if 'metadata_revision_id' not in {column['name'] for column in inspector.get_columns('jobs')}:
        with op.batch_alter_table('jobs', recreate='always') as batch:
            batch.add_column(sa.Column('metadata_revision_id', sa.Integer(), nullable=True))
            batch.create_foreign_key(
                'fk_jobs_metadata_revision_id',
                'library_metadata_revisions',
                ['metadata_revision_id'],
                ['id'],
            )


def downgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    has_column = 'metadata_revision_id' in {column['name'] for column in inspector.get_columns('jobs')}
    foreign_keys = inspector.get_foreign_keys('jobs')
    has_constraint = any(foreign_key.get('name') == 'fk_jobs_metadata_revision_id' for foreign_key in foreign_keys)
    if has_column and has_constraint:
        with op.batch_alter_table('jobs', recreate='always') as batch:
            batch.drop_constraint('fk_jobs_metadata_revision_id', type_='foreignkey')
            batch.drop_column('metadata_revision_id')
