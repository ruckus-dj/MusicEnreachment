from __future__ import annotations

from errno import EXDEV
from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import Base


def _client(tmp_path: Path) -> tuple[TestClient, Path, Path]:
    data = tmp_path / 'data'
    incoming = data / 'incoming'
    media = data / 'media'
    incoming.mkdir(parents=True)
    media.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "storage.db"}')
    Base.metadata.create_all(engine)
    return (
        TestClient(
            create_app(
                lambda: Session(engine),
                incoming_root=incoming,
                source_roots_parent=data,
                media_root=media,
                storage_browse_roots=(data,),
            )
        ),
        data,
        media,
    )


def test_storage_settings_when_browsing_container_tree_only_exposes_safe_directories(tmp_path: Path) -> None:
    # Given: a container-visible mount with a nested directory and a symlink.
    client, data, _ = _client(tmp_path)
    nested = data / 'incoming' / 'albums'
    nested.mkdir()
    (data / 'linked').symlink_to(nested, target_is_directory=True)

    # When: the operator opens the mount root and navigates into an input directory.
    root = client.get('/api/settings/storage/browser')
    child = client.get('/api/settings/storage/browser', params={'path': str(data / 'incoming')})

    # Then: only real directories are visible and navigation preserves the container path.
    assert root.status_code == 200
    assert root.json()['items'] == [
        {'name': 'incoming', 'path': str(data / 'incoming')},
        {'name': 'media', 'path': str(data / 'media')},
    ]
    assert child.status_code == 200
    assert child.json()['path'] == str(data / 'incoming')
    assert child.json()['items'] == [{'name': 'albums', 'path': str(nested)}]


def test_storage_settings_when_output_changes_moves_content_and_rejects_input_overlap(tmp_path: Path) -> None:
    # Given: a configured input root and an output tree containing a published file.
    client, data, media = _client(tmp_path)
    incoming = data / 'incoming'
    (media / 'Artist').mkdir()
    _ = (media / 'Artist' / 'track.flac').write_bytes(b'audio')
    created = client.post('/api/settings/source-roots', json={'path': str(incoming), 'display_name': 'Incoming'})
    assert created.status_code == 201
    new_media = data / 'new-media'
    new_media.mkdir()

    # When: the operator previews and confirms the output relocation.
    preview = client.post('/api/settings/storage/output/preview', json={'path': str(new_media)})
    moved = client.put('/api/settings/storage/output', json={'path': str(new_media)})
    overlap = client.post('/api/settings/source-roots', json={'path': str(new_media / 'Artist'), 'display_name': 'Bad'})

    # Then: the output contents move, the configured root changes, and output descendants cannot become inputs.
    assert preview.status_code == 200
    assert preview.json()['same_filesystem'] is True
    assert moved.status_code == 200
    assert moved.json()['output_root'] == str(new_media)
    assert (new_media / 'Artist' / 'track.flac').read_bytes() == b'audio'
    assert not (media / 'Artist').exists()
    assert overlap.status_code == 422


def test_storage_settings_when_same_device_rename_reports_cross_device_then_copies(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a destination reported on the same device where the mount rejects rename with EXDEV.
    client, data, media = _client(tmp_path)
    (media / 'Artist').mkdir()
    _ = (media / 'Artist' / 'track.flac').write_bytes(b'audio')
    new_media = data / 'new-media'
    new_media.mkdir()

    def reject_rename(source: Path | str, destination: Path | str) -> None:
        raise OSError(EXDEV, 'Invalid cross-device link', source, destination)

    monkeypatch.setattr('music_ingest.storage.os.replace', reject_rename)

    # When: the operator confirms output relocation.
    moved = client.put('/api/settings/storage/output', json={'path': str(new_media)})

    # Then: output contents are copied despite the false same-filesystem signal.
    assert moved.status_code == 200
    assert (new_media / 'Artist' / 'track.flac').read_bytes() == b'audio'
    assert not (media / 'Artist').exists()
