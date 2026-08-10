from __future__ import annotations

import sys
from logging.config import fileConfig
from pathlib import Path

from sqlalchemy import create_engine, make_url, pool

from alembic import context

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'src'))

from music_ingest.models import Base

config = context.config

if config.config_file_name is not None:
    fileConfig(config.config_file_name)

target_metadata = Base.metadata


def run_migrations_offline() -> None:
    context.configure(url=config.get_main_option('sqlalchemy.url'), target_metadata=target_metadata, literal_binds=True)
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    database_url = config.get_main_option('sqlalchemy.url')
    if database_url is None:
        raise RuntimeError('sqlalchemy.url is required')
    connect_args: dict[str, int] = {}
    if make_url(database_url).get_backend_name() == 'postgresql':
        connect_args['connect_timeout'] = int(config.get_main_option('music_ingest.connect_timeout_seconds', '10'))
    connectable = create_engine(database_url, poolclass=pool.NullPool, connect_args=connect_args)
    with connectable.connect() as connection:
        context.configure(connection=connection, target_metadata=target_metadata)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
