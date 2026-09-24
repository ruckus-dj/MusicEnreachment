from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    SourceRecord,
    StorageConfigRecord,
)
from music_ingest.services.publication import (
    PublicationAttemptRequest,
    expose_attempt,
    finalize_and_cleanup_attempt,
    mark_staged,
    reserve_attempt,
)
from music_ingest.services.publication.cleanup import cleanup_attempt


def test_finalization_removes_superseded_audio_and_empty_directories_after_path_change(tmp_path: Path) -> None:
    # Given: metadata moves one current publication to a different artist and album directory.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "path-change.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    old_directory = media_root / 'Old Artist' / 'Old Album'
    target = media_root / 'New Artist' / 'New Album'
    staging = target / '.music-ingest-publications' / 'path-change' / 'staged'
    backup = target / '.music-ingest-publications' / 'path-change' / 'backup'
    old_directory.mkdir(parents=True)
    staging.mkdir(parents=True)
    old_audio = old_directory / '01 - Track.mka'
    _ = old_audio.write_bytes(b'old-output')
    _ = (staging / old_audio.name).write_bytes(b'new-output')
    with Session(engine) as session:
        record, source = _record_and_source('path-change', tmp_path, now)
        session.add_all(
            (
                record,
                source,
                StorageConfigRecord(
                    id=1,
                    output_root=str(media_root),
                    migration_json=None,
                    state='ready',
                    generation=1,
                    updated_at=now,
                ),
                LibraryPublicationRecord(
                    id='publication-old-path',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(old_audio),
                    format_name='mka',
                    content_sha256=sha256(b'old-output').hexdigest(),
                    state='current',
                    created_at=now,
                ),
            )
        )
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-path-change', record.id, source.id, None, target, old_audio.name, staging, backup, now
            ),
        )
        assert attempt is not None
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        session.commit()

        # When: the replacement becomes current and post-commit cleanup runs.
        publication = finalize_and_cleanup_attempt(session, attempt, now)

        # Then: only the replacement is active and the old managed path is pruned to the media root.
        current = session.scalars(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.library_record_id == record.id)
            .where(LibraryPublicationRecord.state == 'current')
        ).all()
        assert [item.id for item in current] == [publication.id]
        assert Path(publication.path).read_bytes() == b'new-output'
        assert not old_audio.exists()
        assert not old_directory.exists()
        assert not old_directory.parent.exists()


def test_cleanup_defers_superseded_path_owned_by_an_exposed_attempt(tmp_path: Path) -> None:
    # Given: another record has exposed the same bytes at a superseded publication path but has not finalized yet.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "protected-attempt.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    protected = media_root / 'Reused Artist' / 'Reused Album' / '01 - Track.mka'
    protected.parent.mkdir(parents=True)
    _ = protected.write_bytes(b'same-output')
    with Session(engine) as session:
        first_record, first_source = _record_and_source('first', tmp_path, now)
        second_record, second_source = _record_and_source('second', tmp_path, now)
        cleanup = _attempt('cleanup', first_record.id, first_source.id, media_root / 'Current', now, 'finalized')
        exposed = _attempt('exposed', second_record.id, second_source.id, protected.parent, now, 'exposed')
        session.add_all(
            (
                first_record,
                first_source,
                second_record,
                second_source,
                StorageConfigRecord(
                    id=1,
                    output_root=str(media_root),
                    migration_json=None,
                    state='ready',
                    generation=1,
                    updated_at=now,
                ),
                LibraryPublicationRecord(
                    id='superseded-first',
                    library_record_id=first_record.id,
                    source_id=first_source.id,
                    path=str(protected),
                    format_name='mka',
                    content_sha256=sha256(b'same-output').hexdigest(),
                    state='superseded',
                    created_at=now,
                ),
                cleanup,
                exposed,
            )
        )
        session.flush()

        # When: cleanup runs while the exposed attempt still owns the destination.
        completed = cleanup_attempt(cleanup, session)

        # Then: cleanup remains retryable and does not delete the exposed output.
        assert not completed
        assert protected.read_bytes() == b'same-output'


def test_cleanup_rejects_superseded_path_through_an_ancestor_symlink(tmp_path: Path) -> None:
    # Given: a historical publication path traverses an in-root symlink to a different managed directory.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "symlink-path.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 24, tzinfo=UTC)
    media_root = tmp_path / 'media'
    victim_directory = media_root / 'victim'
    victim_directory.mkdir(parents=True)
    victim = victim_directory / 'track.mka'
    _ = victim.write_bytes(b'victim-output')
    alias = media_root / 'historical-alias'
    alias.symlink_to(victim_directory, target_is_directory=True)
    with Session(engine) as session:
        record, source = _record_and_source('symlink', tmp_path, now)
        cleanup = _attempt('symlink-cleanup', record.id, source.id, media_root / 'Current', now, 'finalized')
        session.add_all(
            (
                record,
                source,
                StorageConfigRecord(
                    id=1,
                    output_root=str(media_root),
                    migration_json=None,
                    state='ready',
                    generation=1,
                    updated_at=now,
                ),
                LibraryPublicationRecord(
                    id='superseded-symlink',
                    library_record_id=record.id,
                    source_id=source.id,
                    path=str(alias / victim.name),
                    format_name='mka',
                    content_sha256=sha256(b'victim-output').hexdigest(),
                    state='superseded',
                    created_at=now,
                ),
                cleanup,
            )
        )
        session.flush()

        # When: cleanup evaluates the historical path.
        completed = cleanup_attempt(cleanup, session)

        # Then: the symlink target remains untouched.
        assert completed
        assert victim.read_bytes() == b'victim-output'


def _record_and_source(prefix: str, root: Path, now: datetime) -> tuple[LibraryRecord, SourceRecord]:
    record = LibraryRecord(id=f'record-{prefix}', created_at=now, updated_at=now)
    source = SourceRecord(
        id=f'source-{prefix}',
        source_path=str(root / f'{prefix}.flac'),
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        library_record=record,
    )
    return record, source


def _attempt(
    prefix: str,
    library_record_id: str,
    source_id: str,
    target_directory: Path,
    now: datetime,
    state: str,
) -> PublicationAttemptRecord:
    workspace = target_directory / '.music-ingest-publications' / prefix
    return PublicationAttemptRecord(
        id=f'attempt-{prefix}',
        library_record_id=library_record_id,
        source_id=source_id,
        metadata_revision_id=None,
        state=state,
        target_directory=str(target_directory),
        target_audio_name='01 - Track.mka',
        staging_directory=str(workspace / 'staged'),
        backup_directory=str(workspace / 'backup'),
        created_at=now,
        completion_state='complete',
    )
