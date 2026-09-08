from sqlalchemy import text
from sqlalchemy.orm import Session


def acquire_storage_lock(session: Session) -> None:
    """Serialize filesystem writers and recovery; reacquire after every commit."""
    if session.get_bind().dialect.name == 'postgresql':
        session.execute(text('SELECT pg_advisory_xact_lock(732014901)'))
