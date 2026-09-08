from typing import Protocol

from sqlalchemy.orm import Session


class SessionFactory(Protocol):
    """Create a request-owned session; route handlers retain transaction ownership."""

    def __call__(self) -> Session: ...
