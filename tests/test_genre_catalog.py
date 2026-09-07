from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.matching.genre_catalog import load_genre_catalog, replace_genre_catalog
from music_ingest.matching.genres import GenreCatalogEntry
from music_ingest.models import Base


def _engine(tmp_path: Path) -> object:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "genre-catalog.db"}')
    Base.metadata.create_all(engine)
    return engine


def test_replace_genre_catalog_when_given_entries_persists_them_with_normalized_keys(tmp_path: Path) -> None:
    # Given: a fresh catalog and two genre entries from MusicBrainz.
    engine = _engine(tmp_path)
    synced_at = datetime(2026, 8, 4, tzinfo=UTC)
    entries = (
        GenreCatalogEntry(musicbrainz_id='genre-one', source_name='dnb', display_name='DnB'),
        GenreCatalogEntry(musicbrainz_id='genre-two', source_name='hip hop', display_name='Hip Hop'),
    )

    # When: the catalog is replaced and reloaded.
    with Session(engine) as session:
        replace_genre_catalog(session, entries, synced_at)
        session.commit()
    with Session(engine) as session:
        loaded = load_genre_catalog(session)

    # Then: both entries are persisted, sorted by display name, with normalized lookup keys.
    assert [(item.musicbrainz_id, item.display_name, item.normalized_key) for item in loaded] == [
        ('genre-one', 'DnB', 'dnb'),
        ('genre-two', 'Hip Hop', 'hip hop'),
    ]
    assert all(item.synced_at.replace(tzinfo=UTC) == synced_at for item in loaded)


def test_replace_genre_catalog_when_called_again_discards_the_previous_entries(tmp_path: Path) -> None:
    # Given: an existing catalog synced from an earlier MusicBrainz genre list.
    engine = _engine(tmp_path)
    first_sync = datetime(2026, 8, 4, tzinfo=UTC)
    second_sync = datetime(2026, 8, 5, tzinfo=UTC)
    with Session(engine) as session:
        replace_genre_catalog(
            session, (GenreCatalogEntry(musicbrainz_id='genre-old', source_name='idm', display_name='IDM'),), first_sync
        )
        session.commit()

    # When: a fresh sync replaces the catalog with a different genre list.
    with Session(engine) as session:
        replace_genre_catalog(
            session,
            (GenreCatalogEntry(musicbrainz_id='genre-new', source_name='edm', display_name='EDM'),),
            second_sync,
        )
        session.commit()
    with Session(engine) as session:
        loaded = load_genre_catalog(session)

    # Then: only the latest sync's entries remain.
    assert [item.musicbrainz_id for item in loaded] == ['genre-new']
