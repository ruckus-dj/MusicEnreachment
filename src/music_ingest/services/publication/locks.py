from sqlalchemy import text
from sqlalchemy.orm import Session


def acquire_migration_lock(session: Session, *, exclusive: bool = False) -> None:
    """Readers may process concurrently; relocation excludes all handlers and recovery.

    Always acquire this gate before the publication/recovery lock (732014901).
    Transaction scope matches the durable publication intent checkpoint.
    """
    if session.get_bind().dialect.name == 'postgresql':
        query = (
            'SELECT pg_advisory_xact_lock(732014902)' if exclusive else 'SELECT pg_advisory_xact_lock_shared(732014902)'
        )
        session.execute(text(query))


def try_acquire_storage_lock(session: Session) -> bool:
    """Keep the shared relocation gate, but defer rather than queue behind a writer."""
    acquire_migration_lock(session)
    if session.get_bind().dialect.name == 'postgresql':
        return bool(session.scalar(text('SELECT pg_try_advisory_xact_lock(732014901)')))
    return True


def acquire_storage_lock(session: Session) -> None:
    """Serialize filesystem writers and recovery; reacquire after every commit."""
    acquire_migration_lock(session)
    if session.get_bind().dialect.name == 'postgresql':
        session.execute(text('SELECT pg_advisory_xact_lock(732014901)'))
