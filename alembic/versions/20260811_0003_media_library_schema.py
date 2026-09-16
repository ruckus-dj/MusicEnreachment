"""media library source and selection schema

Revision ID: 20260811_0003
Revises: 20260810_0002
"""

from __future__ import annotations

from pathlib import Path

import sqlalchemy as sa

from alembic import context, op

revision = '20260811_0003'
down_revision = '20260810_0002'
branch_labels = None
depends_on = None


def _legacy_root() -> Path:
    arguments = context.get_x_argument(as_dictionary=True)
    raw_root = arguments.get('legacy_incoming_root')
    if raw_root is None:
        raise RuntimeError('legacy_incoming_root is required')
    try:
        root = Path(raw_root).resolve(strict=True)
    except FileNotFoundError as error:
        raise RuntimeError('legacy_incoming_root must exist') from error
    if not root.is_dir():
        raise RuntimeError('legacy_incoming_root must be a readable directory')
    return root


def upgrade() -> None:
    bind = op.get_bind()
    legacy_root = _legacy_root() if bind.dialect.name == 'postgresql' else None
    op.create_table(
        'source_roots',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('display_name', sa.String(255), nullable=False),
        sa.Column('canonical_path', sa.Text(), nullable=False, unique=True),
        sa.Column('enabled', sa.Boolean(), nullable=False),
        sa.Column('scan_state', sa.String(32), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.add_column('source_records', sa.Column('source_root_id', sa.String(96), server_default='legacy', nullable=True))
    op.add_column('source_records', sa.Column('media_codec', sa.String(16)))
    op.add_column('source_records', sa.Column('media_bit_depth', sa.Integer()))
    op.add_column('source_records', sa.Column('media_sample_rate', sa.Integer()))
    op.add_column('source_records', sa.Column('media_channels', sa.Integer()))
    op.add_column('source_records', sa.Column('media_bitrate', sa.Integer()))
    op.create_table(
        'source_recording_assignments',
        sa.Column('id', sa.Integer(), primary_key=True),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('library_record_id', sa.String(96), sa.ForeignKey('library_records.id')),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('actor', sa.String(128), nullable=False),
        sa.Column('rationale', sa.Text()),
        sa.Column('evidence_json', sa.Text(), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.create_table(
        'source_association_overrides',
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), primary_key=True),
        sa.Column('recording_mbid', sa.String(36), nullable=False),
        sa.Column('actor', sa.String(128), nullable=False),
        sa.Column('rationale', sa.Text(), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('cleared_at', sa.DateTime(timezone=True)),
    )
    op.create_table(
        'effective_source_decisions',
        sa.Column('library_record_id', sa.String(96), sa.ForeignKey('library_records.id'), primary_key=True),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id')),
        sa.Column('baseline_source_id', sa.String(64), sa.ForeignKey('source_records.id')),
        sa.Column('policy_version', sa.String(64), nullable=False),
        sa.Column('quality_tuple_json', sa.Text(), nullable=False),
        sa.Column('reason', sa.Text(), nullable=False),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False),
    )
    op.create_table(
        'publication_attempts',
        sa.Column('id', sa.String(96), primary_key=True),
        sa.Column('library_record_id', sa.String(96), sa.ForeignKey('library_records.id'), nullable=False),
        sa.Column('source_id', sa.String(64), sa.ForeignKey('source_records.id'), nullable=False),
        sa.Column('metadata_revision_id', sa.Integer(), sa.ForeignKey('library_metadata_revisions.id')),
        sa.Column('state', sa.String(32), nullable=False),
        sa.Column('target_directory', sa.Text(), nullable=False),
        sa.Column('target_audio_name', sa.String(255), nullable=False),
        sa.Column('staging_directory', sa.Text(), nullable=False),
        sa.Column('backup_directory', sa.Text(), nullable=False),
        sa.Column('manifest_sha256', sa.String(64)),
        sa.Column('output_sha256', sa.String(64)),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('exposed_at', sa.DateTime(timezone=True)),
        sa.Column('finalized_at', sa.DateTime(timezone=True)),
        sa.Column('failure_reason', sa.Text()),
        sa.CheckConstraint(
            "state IN ('reserved', 'staged', 'exposed', 'finalized', 'failed')", name='ck_publication_attempt_state'
        ),
    )
    op.add_column('jobs', sa.Column('library_record_id', sa.String(96), nullable=True))
    if bind.dialect.name == 'postgresql':
        op.create_foreign_key('fk_jobs_library_record', 'jobs', 'library_records', ['library_record_id'], ['id'])
        bind.execute(
            sa.text(
                'ALTER TABLE jobs ADD CONSTRAINT ck_jobs_exactly_one_target '
                'CHECK ((source_id IS NOT NULL) != (library_record_id IS NOT NULL)) NOT VALID'
            )
        )
    op.create_index(
        'uq_active_selection_refresh_job',
        'jobs',
        ['library_record_id', 'kind'],
        unique=True,
        postgresql_where=sa.text("kind = 'selection_refresh' AND state IN ('queued', 'running')"),
    )
    op.create_index(
        'uq_current_library_publication',
        'library_publications',
        ['library_record_id'],
        unique=True,
        postgresql_where=sa.text("state = 'current'"),
    )
    if legacy_root is not None:
        _backfill(bind, legacy_root)
        op.alter_column('source_records', 'source_root_id', nullable=False, server_default='legacy')
    if bind.dialect.name == 'postgresql':
        op.create_foreign_key(
            'fk_source_records_source_root', 'source_records', 'source_roots', ['source_root_id'], ['id']
        )


def _backfill(bind: sa.Connection, legacy_root: Path) -> None:
    now = '2026-08-11T00:00:00+00:00'
    bind.execute(
        sa.text(
            'INSERT INTO source_roots (id, display_name, canonical_path, enabled, scan_state, created_at, updated_at) '
            "VALUES ('legacy', 'legacy', :legacy_path, true, 'never_scanned', :now, :now), "
            "('historical-unmanaged', 'historical-unmanaged', :historical_path, false, 'never_scanned', :now, :now)"
        ),
        {'legacy_path': str(legacy_root), 'historical_path': 'historical-unmanaged://', 'now': now},
    )
    rows = bind.execute(sa.text('SELECT id, source_path, library_record_id FROM source_records')).mappings()
    for row in rows:
        source_path = Path(row['source_path'])
        root_id = 'historical-unmanaged'
        if source_path.exists() and source_path.resolve().is_relative_to(legacy_root):
            root_id = 'legacy'
        bind.execute(
            sa.text('UPDATE source_records SET source_root_id = :root_id WHERE id = :source_id'),
            {'root_id': root_id, 'source_id': row['id']},
        )
        bind.execute(
            sa.text(
                'INSERT INTO source_recording_assignments '
                '(source_id, library_record_id, state, actor, evidence_json, created_at) '
                "VALUES (:source_id, :library_record_id, 'legacy_backfill', 'migration', '{}', :now)"
            ),
            {'source_id': row['id'], 'library_record_id': row['library_record_id'], 'now': now},
        )


def downgrade() -> None:
    bind = op.get_bind()
    op.drop_index('uq_current_library_publication', table_name='library_publications')
    op.drop_index('uq_active_selection_refresh_job', table_name='jobs')
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('ck_jobs_exactly_one_target', 'jobs', type_='check')
        op.drop_constraint('fk_jobs_library_record', 'jobs', type_='foreignkey')
    op.drop_column('jobs', 'library_record_id')
    op.drop_table('publication_attempts')
    op.drop_table('effective_source_decisions')
    op.drop_table('source_association_overrides')
    op.drop_table('source_recording_assignments')
    if bind.dialect.name == 'postgresql':
        op.drop_constraint('fk_source_records_source_root', 'source_records', type_='foreignkey')
    op.drop_column('source_records', 'source_root_id')
    op.drop_column('source_records', 'media_bitrate')
    op.drop_column('source_records', 'media_channels')
    op.drop_column('source_records', 'media_sample_rate')
    op.drop_column('source_records', 'media_bit_depth')
    op.drop_column('source_records', 'media_codec')
    op.drop_table('source_roots')
