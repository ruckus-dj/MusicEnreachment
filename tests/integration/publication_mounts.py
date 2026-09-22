"""Executed inside a disposable Linux container by test_integrity_postgres."""

from __future__ import annotations

import os
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from subprocess import run

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.bootstrap.server import RuntimeConfig, run_migrations
from music_ingest.models import (
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
    StorageConfigRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import append_metadata_revision
from music_ingest.services.publication.workspace import validate_publication_storage
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker


def main() -> None:
    source_root = Path('/sources/incoming')
    source_root.mkdir()
    source_path = source_root / 'source.flac'
    run(
        [
            '/usr/local/bin/ffmpeg',
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-c:a',
            'flac',
            '/sources/incoming/source.flac',
        ],
        check=True,
        timeout=15,
    )
    original = source_path.read_bytes()
    assert Path('/processing').stat().st_dev != Path('/media').stat().st_dev
    validate_publication_storage(Path('/media'))
    runtime = RuntimeConfig.from_environment(os.environ)
    run_migrations(runtime)
    engine = create_engine(runtime.database_url)
    now = datetime.now(UTC)
    stat = source_path.stat()
    with Session(engine) as session:
        storage = session.get(StorageConfigRecord, 1)
        assert storage is not None
        storage.output_root = '/media'
        record = LibraryRecord(id='record', created_at=now, updated_at=now)
        root = SourceRootRecord(
            id='root',
            canonical_path=str(source_root),
            display_name='Root',
            enabled=True,
            scan_state='never_scanned',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source',
            source_path=str(source_path),
            source_root=root,
            device=stat.st_dev,
            inode=stat.st_ino,
            size_bytes=stat.st_size,
            sha256=sha256(original).hexdigest(),
            origin='manual',
            intake_state='present',
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='integration')],
        )
        session.add_all((record, root, source))
        session.commit()
    config = ProcessingConfig(source_root, Path('/processing'), Path('/media'))
    target: Path | None = None
    for genre in ('Rock', 'Jazz'):
        with Session(engine) as session:
            revision = append_metadata_revision(
                session,
                'record',
                'source',
                'final',
                {'TITLE': 'Track', 'ARTIST': 'Artist', 'ALBUM': 'Album', 'GENRE': genre},
                'test',
                now,
            )
            JobRepository(session).enqueue('source', 'final_publish', now, revision.id)
            session.commit()
        with Session(engine) as session:
            assert ProcessingWorker(session, config).run_once()
            session.commit()
            current = session.scalars(
                select(LibraryPublicationRecord).where(LibraryPublicationRecord.state == 'current')
            ).one()
            if target is not None:
                assert target == Path(current.path), 'second publication must replace the same target'
                assert (target.parent / 'album.nfo').read_bytes() == b'preserved'
            target = Path(current.path)
            assert sha256(target.read_bytes()).hexdigest() == current.content_sha256
            (target.parent / 'album.nfo').write_bytes(b'preserved')
            for attempt in session.scalars(select(PublicationAttemptRecord)).all():
                assert attempt.state == 'finalized' and attempt.cleaned_at is not None
                assert Path(attempt.staging_directory).is_relative_to(Path('/media'))
                assert Path(attempt.backup_directory).is_relative_to(Path('/media'))
        assert source_path.read_bytes() == original
    with Session(engine) as session:
        assert len(session.scalars(select(LibraryPublicationRecord)).all()) == 2
    engine.dispose()
    print('separate mounts: initial publication and same-target replacement verified')


if __name__ == '__main__':
    main()
