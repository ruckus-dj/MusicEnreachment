from __future__ import annotations

import os
import shutil
from pathlib import Path
from tempfile import TemporaryDirectory

from music_ingest.services.publication.attempts import _fsync_directory, _fsync_file, _sha256


def durable_directory(path: Path, root: Path) -> None:
    if root.is_symlink() or not path.is_relative_to(root):
        raise ValueError('publication directory must be inside a non-symlink media root')
    current = path
    while current.is_relative_to(root):
        if current.is_symlink():
            raise ValueError(f'publication directory must not be a symbolic link: {current}')
        current = current.parent
    path.mkdir(parents=True, exist_ok=True)
    current = path
    while current.is_relative_to(root):
        _fsync_directory(current)
        current = current.parent
    _fsync_directory(root.parent)


def prepare_publication_copy(source: Path, destination: Path, media_root: Path, target: Path) -> None:
    """Copy disposable output to a durable workspace on the target mount before journaling."""
    durable_directory(destination.parent, media_root)
    if destination.parent.stat().st_dev != target.parent.stat().st_dev:
        raise ValueError('unsupported publication topology: workspace and target must share a filesystem')
    expected = _sha256(source)
    shutil.copyfile(source, destination)
    _fsync_file(destination)
    if _sha256(destination) != expected:
        raise ValueError('publication copy failed SHA-256 verification')
    _fsync_directory(destination.parent)


def validate_publication_storage(media_root: Path) -> None:
    """Fail readiness with a concrete diagnostic when media cannot support durable rename."""
    try:
        durable_directory(media_root, media_root)
        with TemporaryDirectory(prefix='.music-ingest-check-', dir=media_root) as temporary:
            directory = Path(temporary)
            source, target = directory / 'source', directory / 'target'
            source.write_bytes(b'publication topology check')
            _fsync_file(source)
            os.replace(source, target)
            _fsync_directory(directory)
            if target.read_bytes() != b'publication topology check':
                raise ValueError('atomic rename did not preserve content')
        _fsync_directory(media_root)
    except (OSError, ValueError) as error:
        raise ValueError(
            f'unsupported publication storage at {media_root}: writable directories, fsync and atomic rename required: '
            f'{error}'
        ) from error
