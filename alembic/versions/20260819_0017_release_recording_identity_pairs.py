"""allow one recording on multiple releases

Revision ID: 20260819_0017
Revises: 20260818_0016
"""

from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260819_0017'
down_revision = '20260818_0016'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name == 'postgresql':
        unique_constraint = next(
            constraint
            for constraint in sa.inspect(bind).get_unique_constraints('library_records')
            if constraint['column_names'] == ['musicbrainz_recording_id']
        )
        constraint_name = unique_constraint['name']
        if constraint_name is None:
            raise RuntimeError('library_records recording uniqueness constraint has no name')
        op.drop_constraint(constraint_name, 'library_records', type_='unique')
        op.create_unique_constraint(
            'uq_library_records_musicbrainz_recording_release',
            'library_records',
            ['musicbrainz_recording_id', 'musicbrainz_release_id'],
        )
        return
    table = sa.Table('library_records', sa.MetaData(), autoload_with=op.get_bind())
    for constraint in tuple(table.constraints):
        if isinstance(constraint, sa.UniqueConstraint) and tuple(column.name for column in constraint.columns) == (
            'musicbrainz_recording_id',
        ):
            table.constraints.remove(constraint)
    with op.batch_alter_table('library_records', copy_from=table, recreate='always') as batch:
        batch.create_unique_constraint(
            'uq_library_records_musicbrainz_recording_release',
            ['musicbrainz_recording_id', 'musicbrainz_release_id'],
        )


def downgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name == 'postgresql':
        op.drop_constraint(
            'uq_library_records_musicbrainz_recording_release',
            'library_records',
            type_='unique',
        )
        op.create_unique_constraint(
            'uq_library_records_musicbrainz_recording_id',
            'library_records',
            ['musicbrainz_recording_id'],
        )
        return
    table = sa.Table('library_records', sa.MetaData(), autoload_with=op.get_bind())
    for constraint in tuple(table.constraints):
        if isinstance(constraint, sa.UniqueConstraint) and tuple(column.name for column in constraint.columns) == (
            'musicbrainz_recording_id',
            'musicbrainz_release_id',
        ):
            table.constraints.remove(constraint)
    with op.batch_alter_table('library_records', copy_from=table, recreate='always') as batch:
        batch.create_unique_constraint('uq_library_records_musicbrainz_recording_id', ['musicbrainz_recording_id'])
