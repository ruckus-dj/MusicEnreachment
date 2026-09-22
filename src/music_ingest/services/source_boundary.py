from __future__ import annotations

import os
from pathlib import Path
from stat import S_ISREG

from music_ingest.models import SourceRecord


class SourceBoundaryError(ValueError):
    pass


def resolve_regular_file(path: Path, root: Path) -> Path:
    try:
        canonical_root = root.resolve(strict=True)
        root_status = canonical_root.lstat()
        path_status = path.lstat()
    except FileNotFoundError as error:
        raise SourceBoundaryError('source path or root is missing') from error
    if root.is_symlink() or not canonical_root.is_dir() or not S_ISREG(path_status.st_mode):
        raise SourceBoundaryError('source root must be a directory and source must be a regular file')
    if path.is_symlink():
        raise SourceBoundaryError('source path cannot be a symbolic link')
    _ = root_status
    try:
        resolved = path.resolve(strict=True)
    except OSError as error:
        raise SourceBoundaryError('source path cannot be resolved') from error
    if not resolved.is_relative_to(canonical_root):
        raise SourceBoundaryError('source path is outside its configured root')
    descriptor = os.open(resolved, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        if not S_ISREG(os.fstat(descriptor).st_mode):
            raise SourceBoundaryError('source path must be a regular file')
    finally:
        os.close(descriptor)
    return resolved


def resolve_owned_source(source: SourceRecord) -> Path:
    root = source.source_root
    if root is None:
        raise SourceBoundaryError('source root is missing')
    if (
        not root.enabled
        or root.id == 'historical-unmanaged'
        or root.canonical_path.startswith('historical-unmanaged://')
    ):
        raise SourceBoundaryError('source root is disabled or historical')
    return resolve_regular_file(Path(source.source_path), Path(root.canonical_path))
