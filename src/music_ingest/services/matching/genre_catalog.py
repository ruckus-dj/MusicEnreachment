from __future__ import annotations

from datetime import datetime

from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from music_ingest.models import GenreCatalogRecord
from music_ingest.services.matching.genres import GenreCatalogEntry
from music_ingest.services.normalize.genre_names import genre_key


def replace_genre_catalog(session: Session, entries: tuple[GenreCatalogEntry, ...], synced_at: datetime) -> None:
    session.execute(delete(GenreCatalogRecord))
    session.add_all(
        GenreCatalogRecord(
            musicbrainz_id=entry.musicbrainz_id,
            source_name=entry.source_name,
            display_name=entry.display_name,
            normalized_key=genre_key(entry.source_name),
            synced_at=synced_at,
        )
        for entry in entries
    )


def load_genre_catalog(session: Session) -> tuple[GenreCatalogRecord, ...]:
    return tuple(session.scalars(select(GenreCatalogRecord).order_by(GenreCatalogRecord.display_name)).all())
