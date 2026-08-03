import sqlalchemy as sa

from alembic import op

revision = '20260729_0008'
down_revision = '20260729_0007'
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table('webhook_receipts') as batch:
        batch.add_column(
            sa.Column('job_id', sa.String(96), sa.ForeignKey('jobs.id', name='fk_webhook_receipts_job_id'))
        )
    dialect_name = op.get_bind().dialect.name
    if dialect_name == 'postgresql':
        op.create_check_constraint('ck_tag_layer_revision_positive', 'tag_layers', 'revision > 0')
        _install_postgresql_invariants()
    if dialect_name == 'sqlite':
        _install_sqlite_invariants()


def downgrade() -> None:
    dialect_name = op.get_bind().dialect.name
    if dialect_name == 'postgresql':
        _remove_postgresql_invariants()
        op.drop_constraint('ck_tag_layer_revision_positive', 'tag_layers', type_='check')
    if dialect_name == 'sqlite':
        _remove_sqlite_invariants()
    with op.batch_alter_table('webhook_receipts') as batch:
        batch.drop_column('job_id')


def _install_postgresql_invariants() -> None:
    op.execute(
        """
        CREATE FUNCTION enforce_tag_layer_revision() RETURNS trigger AS $$
        BEGIN
            IF NEW.revision <= COALESCE(
                (SELECT MAX(revision) FROM tag_layers
                 WHERE release_file_id = NEW.release_file_id AND layer = NEW.layer),
                0
            ) THEN
                RAISE EXCEPTION 'tag layer revision must be positive and strictly monotonic';
            END IF;
            RETURN NEW;
        END;
        $$ LANGUAGE plpgsql
        """
    )
    op.execute(
        """
        CREATE FUNCTION reject_workflow_history_mutation() RETURNS trigger AS $$
        BEGIN
            RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
        END;
        $$ LANGUAGE plpgsql
        """
    )
    op.execute(
        'CREATE TRIGGER tag_layers_require_monotonic_revision '
        + 'BEFORE INSERT ON tag_layers FOR EACH ROW '
        + 'EXECUTE FUNCTION enforce_tag_layer_revision()'
    )
    for table_name in ('audit_records', 'tombstones'):
        op.execute(
            f'CREATE TRIGGER {table_name}_append_only BEFORE UPDATE OR DELETE ON {table_name} '
            + 'FOR EACH ROW EXECUTE FUNCTION reject_workflow_history_mutation()'
        )


def _remove_postgresql_invariants() -> None:
    for table_name in ('audit_records', 'tombstones'):
        op.execute(f'DROP TRIGGER {table_name}_append_only ON {table_name}')
    op.execute('DROP TRIGGER tag_layers_require_monotonic_revision ON tag_layers')
    op.execute('DROP FUNCTION reject_workflow_history_mutation()')
    op.execute('DROP FUNCTION enforce_tag_layer_revision()')


def _install_sqlite_invariants() -> None:
    op.execute(
        """
        CREATE TRIGGER tag_layers_require_monotonic_revision
        BEFORE INSERT ON tag_layers
        FOR EACH ROW WHEN NEW.revision <= COALESCE(
            (SELECT MAX(revision) FROM tag_layers
             WHERE release_file_id = NEW.release_file_id AND layer = NEW.layer),
            0
        )
        BEGIN SELECT RAISE(ABORT, 'tag layer revision must be positive and strictly monotonic'); END
        """
    )
    for table_name in ('audit_records', 'tombstones'):
        for operation in ('UPDATE', 'DELETE'):
            op.execute(
                f'CREATE TRIGGER {table_name}_{operation.lower()}_append_only '
                + f'BEFORE {operation} ON {table_name} '
                + f"BEGIN SELECT RAISE(ABORT, '{table_name} is append-only'); END"
            )


def _remove_sqlite_invariants() -> None:
    for table_name in ('audit_records', 'tombstones'):
        for operation in ('UPDATE', 'DELETE'):
            op.execute(f'DROP TRIGGER {table_name}_{operation.lower()}_append_only')
    op.execute('DROP TRIGGER tag_layers_require_monotonic_revision')
