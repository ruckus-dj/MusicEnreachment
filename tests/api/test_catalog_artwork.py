from datetime import UTC, datetime
from pathlib import Path

from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base, ReleaseArtworkRecord, StorageConfigRecord


def test_release_artwork_uses_persisted_output_root_after_storage_location_changes(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "catalog-artwork.db"}')
    Base.metadata.create_all(engine)
    startup_root = tmp_path / 'startup-media'
    startup_root.mkdir()
    persisted_root = tmp_path / 'configured-media'
    release_directory = persisted_root / 'artist' / 'album'
    release_directory.mkdir(parents=True)
    artwork_path = release_directory / 'cover.jpg'
    artwork_payload = b'fixture-artwork'
    _ = artwork_path.write_bytes(artwork_payload)
    release_mbid = 'ed4d1f52-2f03-46f5-ac2b-41b7772c1e40'
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            StorageConfigRecord(
                id=1,
                output_root=str(persisted_root),
                state='ready',
                generation=2,
                updated_at=now,
            )
        )
        session.add(
            ReleaseArtworkRecord(
                release_mbid=release_mbid,
                path=str(artwork_path),
                format_name='jpg',
                provider='musicbrainz',
                state='ready',
                created_at=now,
                updated_at=now,
            )
        )
        session.commit()

    response = TestClient(create_app(lambda: Session(engine), media_root=startup_root)).get(
        f'/api/library/release-artwork/{release_mbid}'
    )

    assert response.status_code == 200
    assert response.content == artwork_payload
    assert response.headers['content-type'] == 'image/jpg'
