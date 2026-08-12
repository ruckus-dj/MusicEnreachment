from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = '20260812_0005'
down_revision = '20260812_0004'
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name != 'postgresql':
        return
    inspector = sa.inspect(bind)
    for table_name in inspector.get_table_names(schema='public'):
        for column in inspector.get_columns(table_name, schema='public'):
            if isinstance(column['type'], sa.VARCHAR):
                op.alter_column(
                    table_name,
                    column['name'],
                    schema='public',
                    type_=sa.Text(),
                    existing_type=column['type'],
                    existing_nullable=column['nullable'],
                )


def downgrade() -> None:
    pass
