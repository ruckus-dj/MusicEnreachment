from __future__ import annotations

from datetime import UTC, datetime
from errno import ENOSPC
from hashlib import sha256
from pathlib import Path

import pytest
from sqlalchemy import Engine, create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import Base, LibraryPublicationRecord, LibraryRecord, SourceRecord, StorageConfigRecord
from music_ingest.storage import StorageService, StorageValidationError
from music_ingest.storage_migration import MigrationJournal, resume_storage_migration


def seed_storage(engine: Engine, root: Path) -> tuple[Path, Path]:
    old, new = root / 'media', root / 'new'
    old.mkdir(exist_ok=True)
    new.mkdir()
    now = datetime.now(UTC)
    with Session(engine) as session:
        config = session.get(StorageConfigRecord, 1)
        if config is None:
            session.add(StorageConfigRecord(id=1, output_root=str(old), state='ready', generation=1, updated_at=now))
        for index in range(2):
            path = old / f'{index}.mka'
            path.write_bytes(f'audio-{index}'.encode())
            record = LibraryRecord(id=f'record-{index}', created_at=now, updated_at=now)
            source = SourceRecord(
                id=f'source-{index}',
                source_path=f'/immutable/{index}',
                device=1,
                inode=index,
                size_bytes=1,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
                library_record=record,
            )
            session.add_all((record, source))
            session.flush()
            session.add(
                LibraryPublicationRecord(
                    id=f'publication-{index}',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(path),
                    format_name='mka',
                    content_sha256=sha256(path.read_bytes()).hexdigest(),
                    state='current',
                    created_at=now,
                )
            )
        session.commit()
    (old / 'album.nfo').write_bytes(b'never delete')
    (old / 'unmanaged.txt').write_bytes(b'keep original')
    return old, new


def finish_migration(engine: Engine) -> None:
    for _ in range(30):
        with Session(engine) as session:
            if not resume_storage_migration(session):
                return
    pytest.fail('migration did not finish')


@pytest.mark.parametrize('failure', ['second-copy', 'disk-full', 'after-rename', 'switch-commit', 'cleanup-commit'])
def test_storage_migration_resumes_without_losing_old_files(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    failure: str,
) -> None:
    engine = create_engine(f'sqlite:///{tmp_path / "storage.db"}')
    Base.metadata.create_all(engine)
    old, new = seed_storage(engine, tmp_path)
    with Session(engine) as session:
        service = StorageService(session, (tmp_path,), old)
        pending = service.move_output(str(new))
        assert pending.state == 'migrating'
        session.commit()
        assert service.move_output(str(new)).migration_json == pending.migration_json
        with pytest.raises(StorageValidationError, match='another output migration'):
            service.move_output(str(old))
    # Finish first copy, then inject the second-file error or a switch/cleanup commit failure.
    with Session(engine) as session:
        assert resume_storage_migration(session)
    if failure == 'after-rename':
        import os

        replace = os.replace

        def interrupt_rename(source: Path, destination: Path) -> None:
            replace(source, destination)
            raise OSError('injected after rename')

        with monkeypatch.context() as patch:
            patch.setattr('music_ingest.storage_migration.os.replace', interrupt_rename)
            with Session(engine) as session, pytest.raises(OSError, match='injected'):
                resume_storage_migration(session)
        assert (new / '1.mka').read_bytes() == b'audio-1'
    elif failure in {'second-copy', 'disk-full'}:
        with monkeypatch.context() as patch:

            def fail_copy(source: Path, destination: Path) -> None:
                destination.write_bytes(b'partial')
                raise OSError(ENOSPC if failure == 'disk-full' else 5, 'injected copy failure')

            patch.setattr('music_ingest.storage_migration.shutil.copyfile', fail_copy)
            with Session(engine) as session, pytest.raises(OSError, match='injected'):
                resume_storage_migration(session)
        assert (old / '0.mka').read_bytes() == b'audio-0'
        assert (old / '1.mka').read_bytes() == b'audio-1'
    else:
        for _ in range(3):
            with Session(engine) as session:
                resume_storage_migration(session)
        if failure == 'cleanup-commit':
            with Session(engine) as session:
                resume_storage_migration(session)
        with Session(engine) as session:

            def fail_commit() -> None:
                raise OSError('injected commit')

            monkeypatch.setattr(session, 'commit', fail_commit)
            with pytest.raises(OSError, match='injected'):
                resume_storage_migration(session)
            session.rollback()
    with Session(engine) as session:
        config = session.get(StorageConfigRecord, 1)
        assert config is not None and config.state == 'migrating'
        if failure != 'cleanup-commit':
            assert config.output_root == str(old)
    finish_migration(engine)
    with Session(engine) as session:
        config = session.get(StorageConfigRecord, 1)
        assert config is not None and config.state == 'ready' and config.generation == 2
        assert MigrationJournal.model_validate_json(config.migration_json or '{}').phase == 'complete'
        for publication in session.scalars(select(LibraryPublicationRecord)).all():
            assert Path(publication.path).parent == new
            assert _hash(Path(publication.path)) == publication.content_sha256
    assert not (old / '0.mka').exists() and not (old / '1.mka').exists()
    assert (old / 'album.nfo').read_bytes() == (new / 'album.nfo').read_bytes() == b'never delete'
    assert (old / 'unmanaged.txt').read_bytes() == b'keep original'


def _hash(path: Path) -> str:
    return sha256(path.read_bytes()).hexdigest()
