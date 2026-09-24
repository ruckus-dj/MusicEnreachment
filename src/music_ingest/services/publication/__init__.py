from hashlib import sha256
from pathlib import Path
from typing import Final

from sqlalchemy import text
from sqlalchemy.orm import Session

from .attempts import (
    PublicationAttemptRequest,
    expose_attempt,
    finalize_and_cleanup_attempt,
    finalize_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)
from .cleanup import cleanup_attempt

_POSTGRESQL_DIALECT: Final = 'postgresql'


def acquire_publication_destination_lock(session: Session, destination: Path) -> None:
    """Serialize publication operations targeting one managed audio file."""
    if session.get_bind().dialect.name != _POSTGRESQL_DIALECT:
        return
    lock_key = int.from_bytes(
        sha256(str(destination.resolve(strict=False)).encode()).digest()[:8], byteorder='big', signed=True
    )
    _ = session.execute(text('SELECT pg_advisory_xact_lock(:lock_key)'), {'lock_key': lock_key})


def try_acquire_publication_destination_lock(session: Session, destination: Path) -> bool:
    """Try to serialize one audio publication without blocking a worker slot."""
    if session.get_bind().dialect.name != _POSTGRESQL_DIALECT:
        return True
    lock_key = int.from_bytes(
        sha256(str(destination.resolve(strict=False)).encode()).digest()[:8], byteorder='big', signed=True
    )
    return bool(
        session.execute(text('SELECT pg_try_advisory_xact_lock(:lock_key)'), {'lock_key': lock_key}).scalar_one()
    )


__all__ = [
    'PublicationAttemptRequest',
    'acquire_publication_destination_lock',
    'cleanup_attempt',
    'expose_attempt',
    'finalize_attempt',
    'finalize_and_cleanup_attempt',
    'mark_staged',
    'reconcile_attempts',
    'reserve_attempt',
    'try_acquire_publication_destination_lock',
]
