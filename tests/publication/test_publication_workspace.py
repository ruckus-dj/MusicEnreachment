from pathlib import Path

import pytest

from music_ingest.services.publication.workspace import prepare_publication_copy, validate_publication_storage


def test_publication_copy_rejects_corrupted_destination_before_exposure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'processing.mka'
    source.write_bytes(b'validated output')
    target = tmp_path / 'media' / 'audio.mka'
    target.parent.mkdir()
    target.write_bytes(b'old output')
    destination = target.parent / 'workspace' / 'audio.mka'

    def corrupt_copy(source: Path, destination: Path) -> None:
        destination.write_bytes(b'corrupted copy')

    monkeypatch.setattr('music_ingest.services.publication.workspace.shutil.copyfile', corrupt_copy)
    with pytest.raises(ValueError, match='SHA-256'):
        prepare_publication_copy(source, destination, target.parent, target)
    assert target.read_bytes() == b'old output'
    assert source.read_bytes() == b'validated output'


def test_publication_storage_reports_unsupported_rename(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    def reject_rename(source: Path, target: Path) -> None:
        raise OSError('rename unavailable')

    monkeypatch.setattr('music_ingest.services.publication.workspace.os.replace', reject_rename)
    with pytest.raises(ValueError, match='unsupported publication storage.*atomic rename required'):
        validate_publication_storage(tmp_path)
    assert list(tmp_path.iterdir()) == []
