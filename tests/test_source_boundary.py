from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import Base, SourceRecord, SourceRootRecord
from music_ingest.services.source_boundary import SourceBoundaryError, resolve_owned_source, resolve_regular_file


def test_resolve_regular_file_when_path_is_inside_root_returns_the_resolved_path(tmp_path: Path) -> None:
    # Given: a root directory containing a regular file.
    root = tmp_path / 'root'
    root.mkdir()
    target = root / 'song.flac'
    _ = target.write_bytes(b'fixture')

    # When: resolving the file against its owning root.
    resolved = resolve_regular_file(target, root)

    # Then: the canonical path is returned unchanged in identity.
    assert resolved == target.resolve()


def test_resolve_regular_file_when_path_escapes_root_via_traversal_is_rejected(tmp_path: Path) -> None:
    # Given: a file that lives outside the configured root.
    root = tmp_path / 'root'
    root.mkdir()
    outside = tmp_path / 'outside'
    outside.mkdir()
    target = outside / 'song.flac'
    _ = target.write_bytes(b'fixture')
    traversal_path = root / '..' / 'outside' / 'song.flac'

    # When / Then: resolving a path that escapes the root through '..' traversal is rejected.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(traversal_path, root)


def test_resolve_regular_file_when_root_is_a_symlink_is_rejected(tmp_path: Path) -> None:
    # Given: a root that is itself a symlink to a real directory.
    real_root = tmp_path / 'real-root'
    real_root.mkdir()
    target = real_root / 'song.flac'
    _ = target.write_bytes(b'fixture')
    linked_root = tmp_path / 'linked-root'
    linked_root.symlink_to(real_root, target_is_directory=True)

    # When / Then: a symlinked root is never trusted as a boundary.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(linked_root / 'song.flac', linked_root)


def test_resolve_regular_file_when_source_path_is_a_symlink_is_rejected(tmp_path: Path) -> None:
    # Given: a real file outside the root, symlinked to from inside the root.
    root = tmp_path / 'root'
    root.mkdir()
    outside = tmp_path / 'outside'
    outside.mkdir()
    real_target = outside / 'song.flac'
    _ = real_target.write_bytes(b'fixture')
    linked_source = root / 'song.flac'
    linked_source.symlink_to(real_target)

    # When / Then: a symlinked source path is rejected even though it resolves inside the root's parent chain.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(linked_source, root)


def test_resolve_regular_file_when_source_path_is_a_directory_is_rejected(tmp_path: Path) -> None:
    # Given: a directory sitting where a regular file is expected.
    root = tmp_path / 'root'
    root.mkdir()
    nested_dir = root / 'nested'
    nested_dir.mkdir()

    # When / Then: directories are never treated as owned source files.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(nested_dir, root)


def test_resolve_regular_file_when_root_is_missing_is_rejected(tmp_path: Path) -> None:
    # Given: a root path that was never created.
    root = tmp_path / 'missing-root'
    target = root / 'song.flac'

    # When / Then: a missing root cannot be resolved into a boundary.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(target, root)


def test_resolve_regular_file_when_source_path_is_missing_is_rejected(tmp_path: Path) -> None:
    # Given: a root directory without the requested file.
    root = tmp_path / 'root'
    root.mkdir()
    missing = root / 'missing.flac'

    # When / Then: a missing source path cannot be resolved.
    with pytest.raises(SourceBoundaryError):
        _ = resolve_regular_file(missing, root)


def _seeded_source(session: Session, *, source_path: Path, root: SourceRootRecord) -> SourceRecord:
    source = SourceRecord(
        id='source-boundary',
        source_path=str(source_path),
        device=1,
        inode=2,
        size_bytes=3,
        sha256='a' * 64,
        origin='manual',
        intake_state='present',
        source_root=root,
    )
    session.add_all((root, source))
    session.commit()
    return source


def test_resolve_owned_source_when_root_is_enabled_returns_the_resolved_path(tmp_path: Path) -> None:
    # Given: a durable, enabled source root owning a regular file.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "boundary.db"}')
    Base.metadata.create_all(engine)
    root_dir = tmp_path / 'incoming'
    root_dir.mkdir()
    song_path = root_dir / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        root = SourceRootRecord(
            id='incoming-root',
            display_name='Incoming',
            canonical_path=str(root_dir),
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        source = _seeded_source(session, source_path=song_path, root=root)

        # When: resolving the source owned by an enabled root.
        resolved = resolve_owned_source(source)

    # Then: the canonical on-disk path is returned.
    assert resolved == song_path.resolve()


def test_resolve_owned_source_when_root_is_disabled_is_rejected(tmp_path: Path) -> None:
    # Given: a source whose owning root has been disabled by an operator.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "boundary.db"}')
    Base.metadata.create_all(engine)
    root_dir = tmp_path / 'incoming'
    root_dir.mkdir()
    song_path = root_dir / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        root = SourceRootRecord(
            id='incoming-root',
            display_name='Incoming',
            canonical_path=str(root_dir),
            enabled=False,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        source = _seeded_source(session, source_path=song_path, root=root)

        # When / Then: a disabled root can no longer be trusted as a boundary.
        with pytest.raises(SourceBoundaryError):
            _ = resolve_owned_source(source)


def test_resolve_owned_source_when_root_is_historical_unmanaged_is_rejected(tmp_path: Path) -> None:
    # Given: a source attached to the sentinel historical-unmanaged root.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "boundary.db"}')
    Base.metadata.create_all(engine)
    root_dir = tmp_path / 'incoming'
    root_dir.mkdir()
    song_path = root_dir / 'song.flac'
    _ = song_path.write_bytes(b'fixture')
    timestamp = datetime(2026, 8, 4, tzinfo=UTC)

    with Session(engine) as session:
        root = SourceRootRecord(
            id='historical-unmanaged',
            display_name='Historical',
            canonical_path='historical-unmanaged://legacy',
            enabled=True,
            scan_state='scanned',
            created_at=timestamp,
            updated_at=timestamp,
        )
        source = _seeded_source(session, source_path=song_path, root=root)

        # When / Then: the historical-unmanaged sentinel root never grants filesystem access.
        with pytest.raises(SourceBoundaryError):
            _ = resolve_owned_source(source)
