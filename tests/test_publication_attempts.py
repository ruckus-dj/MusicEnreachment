from __future__ import annotations

import json
import os
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

import music_ingest.publication.attempts as attempt_operations
from music_ingest.library.service import append_metadata_revision
from music_ingest.models import (
    Base,
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
)
from music_ingest.models.jobs import JobRepository
from music_ingest.publication import (
    PublicationAttemptRequest,
    cleanup_attempt,
    expose_attempt,
    finalize_and_cleanup_attempt,
    finalize_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)


def test_publication_attempt_finalizes_exposed_manifest_and_supersedes_current_output(tmp_path: Path) -> None:
    # Given: a reserved replacement with a complete staged managed directory.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "attempt.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-1'
    target = tmp_path / 'media' / 'record-1'
    backup = tmp_path / 'backup' / 'attempt-1'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record = LibraryRecord(id='record-1', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-1',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-1', record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)

        # When: the staged directory is atomically exposed and finalized.
        expose_attempt(session, attempt, now)
        publication = finalize_attempt(session, attempt, now)
        session.commit()

        # Then: the new hash is current, the old directory remains recoverable until cleanup, and history is durable.
        assert publication.content_sha256 == sha256(b'new-output').hexdigest()
        persisted_attempt = session.get(PublicationAttemptRecord, attempt.id)
        assert persisted_attempt is not None and persisted_attempt.state == 'finalized'
        assert (staging / 'manifest.json').is_file()
        assert (target / 'audio.flac').read_bytes() == b'new-output'
        assert backup.is_dir()


def test_finalization_commits_before_cleanup_and_is_idempotent(tmp_path: Path) -> None:
    # Given: an exposed replacement and one immutable current publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "finalize-order.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-order'
    target = tmp_path / 'media' / 'record-order'
    backup = tmp_path / 'backup' / 'attempt-order'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-order', 'source-order', tmp_path, now)
        session.add_all((record, source))
        old = LibraryPublicationRecord(
            id='publication-old',
            library_record_id=record.id,
            source_id=source.id,
            path=str(target / 'audio.flac'),
            format_name='flac',
            content_sha256=sha256(b'old-output').hexdigest(),
            state='current',
            created_at=now,
        )
        session.add(old)
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-order',
                record.id,
                source.id,
                append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'New'}, 'test', now).id,
                target,
                'audio.flac',
                staging,
                backup,
                now,
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        session.commit()

        # When: finalization runs twice, with cleanup only after the first commit.
        publication = finalize_and_cleanup_attempt(session, attempt, now)
        retry = finalize_and_cleanup_attempt(session, attempt, now)
        session.commit()

        # Then: one immutable history row remains current and retry returns it unchanged.
        publications = session.scalars(
            select(LibraryPublicationRecord).where(LibraryPublicationRecord.library_record_id == record.id)
        ).all()
        assert [(item.id, item.state) for item in publications] == [
            ('publication-old', 'superseded'),
            (publication.id, 'current'),
        ]
        assert retry.id == publication.id
        persisted_attempt = session.get(PublicationAttemptRecord, attempt.id)
        assert persisted_attempt is not None and persisted_attempt.state == 'finalized'
        assert not staging.exists()
        assert not backup.exists()


def test_finalization_rollback_preserves_transient_paths(tmp_path: Path) -> None:
    # Given: an exposed replacement whose database transaction has not committed.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "rollback.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-rollback'
    target = tmp_path / 'media' / 'record-rollback'
    backup = tmp_path / 'backup' / 'attempt-rollback'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-rollback', 'source-rollback', tmp_path, now)
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-rollback', record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)

        # When: finalization is rolled back before its caller commits.
        finalize_attempt(session, attempt, now)
        session.rollback()

        # Then: transient paths and the pre-finalization database state remain recoverable.
        assert backup.is_dir()
        assert target.is_dir()
        assert staging.exists()
        assert session.get(PublicationAttemptRecord, attempt.id) is None


@pytest.mark.parametrize(
    ('checkpoint', 'expected_output', 'expected_state'),
    (
        ('before-staging', b'old-output', 'failed'),
        ('after-staging', b'old-output', 'failed'),
        ('after-exposure', b'new-output', 'finalized'),
        ('before-commit', b'new-output', 'finalized'),
    ),
)
def test_publication_attempt_crash_matrix_recovers_complete_destination(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    checkpoint: str,
    expected_output: bytes,
    expected_state: str,
) -> None:
    # Given: a publication attempt interrupted at one publication checkpoint.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / f"{checkpoint}.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / checkpoint
    target = tmp_path / 'media' / checkpoint
    backup = tmp_path / 'backup' / checkpoint
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records(checkpoint, f'source-{checkpoint}', tmp_path, now)
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                checkpoint, record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        if checkpoint == 'before-staging':
            session.commit()
        else:
            mark_staged(session, attempt, now)
            if checkpoint == 'after-staging':
                session.commit()
            else:
                expose_attempt(session, attempt, now)
                if checkpoint == 'after-exposure':
                    attempt.state = 'staged'
                    session.commit()
                else:
                    original_commit = session.commit
                    monkeypatch.setattr(session, 'commit', lambda: (_ for _ in ()).throw(OSError('injected')))
                    with pytest.raises(OSError, match='injected'):
                        finalize_and_cleanup_attempt(session, attempt, now)
                    session.rollback()
                    monkeypatch.setattr(session, 'commit', original_commit)
                    original_commit()

        # When: restart reconciliation runs after the injected interruption.
        reconcile_attempts(session, now)
        session.commit()

        # Then: only a complete old or validated new destination remains.
        assert (target / 'audio.flac').read_bytes() == expected_output
        assert attempt.state == expected_state
        assert staging.exists() is (checkpoint == 'before-commit')
        if checkpoint == 'before-commit':
            assert backup.is_dir()
        else:
            assert not backup.exists()
        if expected_output == b'new-output':
            assert not (target / 'manifest.json').exists()


def test_expose_attempt_atomically_replaces_managed_directory_with_persisted_manifest(tmp_path: Path) -> None:
    # Given: a staged replacement and an existing managed destination.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "atomic.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-atomic'
    target = tmp_path / 'media' / 'record-atomic'
    backup = tmp_path / 'backup' / 'attempt-atomic'
    staging.mkdir(parents=True)
    target.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record = LibraryRecord(id='record-atomic', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-atomic',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-atomic', record.id, source.id, 7, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)

        # When: the valid staged directory is exposed.
        expose_attempt(session, attempt, now)

        # Then: the destination is complete, manifest fields are durable, and the old output is retained.
        manifest = json.loads((staging / 'manifest.json').read_text(encoding='utf-8'))
        assert (target / 'audio.flac').read_bytes() == b'new-output'
        assert manifest == {
            'attempt_id': 'attempt-atomic',
            'metadata_revision_id': 7,
            'output_sha256': sha256(b'new-output').hexdigest(),
            'source_id': 'source-atomic',
        }
        assert backup.is_dir()
        assert (backup / 'audio.flac').read_bytes() == b'old-output'
        assert staging.exists()


def test_publication_attempt_when_exposed_output_is_tampered_restores_persisted_backup(tmp_path: Path) -> None:
    # Given: an exposed output whose durable backup still contains the old publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "attempt-recovery.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-2'
    target = tmp_path / 'media' / 'record-2'
    backup = tmp_path / 'backup' / 'attempt-2'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record = LibraryRecord(id='record-2', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-2',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-2', record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        _ = (target / 'audio.flac').write_bytes(b'tampered-output')

        # When: startup recovery sees the exposed hash mismatch.
        reconcile_attempts(session, now)
        session.commit()

        # Then: the old publication is restored and the attempt records its durable failure.
        persisted = session.get(PublicationAttemptRecord, attempt.id)
        assert persisted is not None and persisted.state == 'failed'
        assert (target / 'audio.flac').read_bytes() == b'old-output'


def test_publication_attempt_when_restart_precedes_exposure_discards_staging(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "attempt-staged.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-3'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'never-exposed')
    with Session(engine) as session:
        record = LibraryRecord(id='record-3', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-3',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-3',
                record.id,
                source.id,
                None,
                tmp_path / 'media' / 'record-3',
                'audio.flac',
                staging,
                tmp_path / 'backup' / 'attempt-3',
                now,
            ),
        )
        mark_staged(session, attempt, now)
        reconcile_attempts(session, now)
        session.commit()
        assert attempt.state == 'failed'
        assert not staging.exists()


def test_publication_attempt_when_restart_cleans_reserved_and_staged_paths_without_touching_current(
    tmp_path: Path,
) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "attempt-restart.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    target = tmp_path / 'media' / 'record-4'
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'current-output')
    attempts = (
        ('attempt-4-reserved', 'reserved'),
        ('attempt-4-staged', 'staged'),
    )
    with Session(engine) as session:
        record = LibraryRecord(id='record-4', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-4',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        persisted_attempts = []
        for attempt_id, state in attempts:
            staging = tmp_path / 'staging' / attempt_id
            backup = tmp_path / 'backup' / attempt_id
            staging.mkdir(parents=True)
            backup.mkdir(parents=True)
            _ = (staging / 'audio.flac').write_bytes(b'abandoned-output')
            attempt = reserve_attempt(
                session,
                PublicationAttemptRequest(
                    attempt_id,
                    record.id,
                    source.id,
                    append_metadata_revision(
                        session, record.id, source.id, 'final', {'TITLE': attempt_id}, 'test', now
                    ).id,
                    target,
                    'audio.flac',
                    staging,
                    backup,
                    now,
                ),
            )
            if state == 'staged':
                mark_staged(session, attempt, now)
            persisted_attempts.append(attempt)

        reconcile_attempts(session, now)
        reconcile_attempts(session, now)
        session.commit()

        assert all(attempt.state == 'failed' for attempt in persisted_attempts)
        assert all(not Path(attempt.staging_directory).exists() for attempt in persisted_attempts)
        assert all(not Path(attempt.backup_directory).exists() for attempt in persisted_attempts)
        assert (target / 'audio.flac').read_bytes() == b'current-output'


def test_reconcile_after_target_moves_to_backup_restores_old_output(tmp_path: Path) -> None:
    # Given: a staged attempt interrupted after the old target moved to backup.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "swap-before-target.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-swap-1'
    target = tmp_path / 'media' / 'record-swap-1'
    backup = tmp_path / 'backup' / 'attempt-swap-1'
    staging.mkdir(parents=True)
    target.mkdir(parents=True)
    backup.parent.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-swap-1', 'source-swap-1', tmp_path, now)
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-swap-1', record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)
        backup.mkdir(parents=True, exist_ok=True)
        _ = os.replace(target / 'audio.flac', backup / 'audio.flac')

        # When: restart reconciliation runs before the staging directory is exposed.
        reconcile_attempts(session, now)
        session.commit()

        # Then: the valid old output survives and both transient paths are cleaned.
        assert (target / 'audio.flac').read_bytes() == b'old-output'
        assert not backup.exists()
        assert not staging.exists()


def test_reconcile_after_target_exposure_finalizes_valid_output(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given: a valid target exposed before the database state reached finalization.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "swap-after-target.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-swap-2'
    target = tmp_path / 'media' / 'record-swap-2'
    backup = tmp_path / 'backup' / 'attempt-swap-2'
    staging.mkdir(parents=True)
    target.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-swap-2', 'source-swap-2', tmp_path, now)
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-swap-2', record.id, source.id, None, target, 'audio.flac', staging, backup, now
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        attempt.state = 'staged'

        # When: restart reconciliation sees the exposed manifest and output.
        synced: list[Path] = []
        sync_directory = attempt_operations._fsync_directory

        def sync(path: Path) -> None:
            sync_directory(path)
            synced.append(path)

        monkeypatch.setattr(attempt_operations, '_fsync_directory', sync)
        reconcile_attempts(session, now)
        assert target in synced, 'recovered rename must be fsynced before backup cleanup'

        session.commit()

        # Then: valid output is retained and the attempt is finalized.
        assert (target / 'audio.flac').read_bytes() == b'new-output'
        assert attempt.state == 'finalized'
        cleanup_attempt(attempt)
        assert not backup.exists()


def _publication_records(
    record_id: str, source_id: str, root: Path, now: datetime
) -> tuple[LibraryRecord, SourceRecord]:
    record = LibraryRecord(id=record_id, created_at=now, updated_at=now)
    source = SourceRecord(
        id=source_id,
        source_path=str(root / f'{source_id}.flac'),
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


def test_cleanup_failure_remains_pending_and_preserves_nfo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    engine = create_engine(f'sqlite:///{tmp_path / "cleanup.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    staging, backup, target = tmp_path / 'staging', tmp_path / 'backup', tmp_path / 'media'
    staging.mkdir()
    target.mkdir()
    (staging / 'audio.flac').write_bytes(b'new')
    (target / 'audio.flac').write_bytes(b'old')
    with Session(engine) as session:
        record, source = _publication_records('record', 'source', tmp_path, now)
        session.add_all((record, source))
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt',
                record.id,
                source.id,
                None,
                target,
                'audio.flac',
                staging,
                backup,
                now,
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        finalize_attempt(session, attempt, now)
        session.commit()
        (backup / 'keep.nfo').write_bytes(b'never delete')
        unlink = Path.unlink

        def fail_unlink(path: Path, missing_ok: bool = False) -> None:
            if path.name == 'manifest.json':
                raise PermissionError('injected cleanup failure')
            unlink(path, missing_ok=missing_ok)

        with monkeypatch.context() as patch:
            patch.setattr(Path, 'unlink', fail_unlink)
            with pytest.raises(PermissionError, match='injected'):
                reconcile_attempts(session, now)
        session.rollback()
        assert attempt.state == 'finalized' and attempt.cleaned_at is None
        reconcile_attempts(session, now)
        assert attempt.cleaned_at is not None
        assert (backup / 'keep.nfo').read_bytes() == b'never delete'
        assert (target / 'audio.flac').read_bytes() == b'new'


def test_finalize_attempt_enqueues_one_lrclib_fetch_and_resets_lyric_state_for_new_publication(
    tmp_path: Path,
) -> None:
    # Given: a record whose current publication already owns validated synced lyrics.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-queue.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 11, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-lyric-queue'
    target = tmp_path / 'media' / 'record-lyric-queue'
    backup = tmp_path / 'backup' / 'attempt-lyric-queue'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-lyric-queue', 'source-lyric-queue', tmp_path, now)
        record.lyrics_status = 'synced'
        record.lyrics_path = 'Fixture Artist/Fixture Album/old.lrc'
        record.lyrics_publication_id = 'publication-old'
        record.lyrics_sha256 = 'c' * 64
        record.lyrics_updated_at = now
        session.add_all((record, source))
        session.add(
            LibraryPublicationRecord(
                id='publication-old',
                library_record_id=record.id,
                source_id=source.id,
                path=str(target / 'audio.flac'),
                format_name='flac',
                content_sha256=sha256(b'old-output').hexdigest(),
                state='current',
                created_at=now,
            )
        )
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-lyric-queue',
                record.id,
                source.id,
                append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'New'}, 'test', now).id,
                target,
                'audio.flac',
                staging,
                backup,
                now,
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)

        # Then: an exposed output alone never queues lyric work or drops the current lyric state.
        assert session.query(JobRecord).filter_by(kind='lrclib_fetch').count() == 0
        unchanged = session.get(LibraryRecord, record.id)
        assert unchanged is not None and unchanged.lyrics_status == 'synced'

        # When: the exposed replacement becomes the current publication.
        publication = finalize_attempt(session, attempt, now)
        session.commit()

        # Then: exactly one fetch is queued for the record and lyric state is pending for the new publication.
        persisted = session.get(LibraryRecord, record.id)
        previous = session.get(LibraryPublicationRecord, 'publication-old')
        assert persisted is not None and previous is not None
        assert publication.state == 'current'
        assert previous.state == 'superseded'
        jobs = session.query(JobRecord).filter_by(kind='lrclib_fetch').all()
        assert [(job.library_record_id, job.source_id, job.state) for job in jobs] == [
            ('record-lyric-queue', None, 'queued')
        ]
        assert persisted.lyrics_status == 'pending'
        assert persisted.lyrics_path is None
        assert persisted.lyrics_publication_id is None
        assert persisted.lyrics_sha256 is None
        assert persisted.lyrics_updated_at is not None
        assert persisted.lyrics_updated_at.replace(tzinfo=UTC) == now
        assert persisted.updated_at.replace(tzinfo=UTC) == now
        assert session.query(ReviewDecisionRecord).count() == 0


def test_finalize_attempt_coalesces_lrclib_fetch_across_superseded_publications(tmp_path: Path) -> None:
    # Given: a record whose queued lyric fetch is still active when a replacement is published.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-coalesce.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 11, tzinfo=UTC)
    later = now + timedelta(minutes=5)
    target = tmp_path / 'media' / 'record-lyric-coalesce'
    backup = tmp_path / 'backup'
    for staging_name, payload in (('attempt-lyric-first', b'first-output'), ('attempt-lyric-second', b'second-output')):
        staging = tmp_path / 'staging' / staging_name
        staging.mkdir(parents=True)
        _ = (staging / 'audio.flac').write_bytes(payload)
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-lyric-coalesce', 'source-lyric-coalesce', tmp_path, now)
        session.add_all((record, source))
        first = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-lyric-first',
                record.id,
                source.id,
                None,
                target,
                'audio.flac',
                tmp_path / 'staging' / 'attempt-lyric-first',
                backup / 'attempt-lyric-first',
                now,
            ),
        )
        mark_staged(session, first, now)
        expose_attempt(session, first, now)
        first_publication = finalize_attempt(session, first, now)
        first_job = session.query(JobRecord).filter_by(kind='lrclib_fetch').one()

        # When: a second publication supersedes the first while that fetch is still queued.
        second = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-lyric-second',
                record.id,
                source.id,
                append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Second'}, 'test', later).id,
                target,
                'audio.flac',
                tmp_path / 'staging' / 'attempt-lyric-second',
                backup / 'attempt-lyric-second',
                later,
            ),
        )
        mark_staged(session, second, later)
        expose_attempt(session, second, later)
        second_publication = finalize_attempt(session, second, later)
        session.commit()

        # Then: one superseded publication, one current publication, and a single coalesced active fetch.
        first_persisted = session.get(LibraryPublicationRecord, first_publication.id)
        second_persisted = session.get(LibraryPublicationRecord, second_publication.id)
        assert first_persisted is not None and second_persisted is not None
        assert second_publication.id != first_publication.id
        assert first_persisted.state == 'superseded'
        assert second_persisted.state == 'current'
        jobs = session.query(JobRecord).filter_by(kind='lrclib_fetch').all()
        assert [job.id for job in jobs] == [first_job.id]
        assert jobs[0].state == 'queued'
        refreshed = session.get(LibraryRecord, record.id)
        assert refreshed is not None
        assert refreshed.lyrics_status == 'pending'
        assert refreshed.lyrics_updated_at is not None
        assert refreshed.lyrics_updated_at.replace(tzinfo=UTC) == later


def test_finalize_attempt_queues_lrclib_fetch_in_the_publication_transaction(tmp_path: Path) -> None:
    # Given: a durable record whose exposed replacement is finalized without its caller committing.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-transaction.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 11, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-lyric-transaction'
    target = tmp_path / 'media' / 'record-lyric-transaction'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-lyric-transaction', 'source-lyric-transaction', tmp_path, now)
        session.add_all((record, source))
        session.commit()
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-lyric-transaction',
                record.id,
                source.id,
                None,
                target,
                'audio.flac',
                staging,
                tmp_path / 'backup' / 'attempt-lyric-transaction',
                now,
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)
        _ = finalize_attempt(session, attempt, now)

        # Then: the current publication, the queued fetch, and the lyric reset are one durable unit of work.
        assert session.query(JobRecord).filter_by(kind='lrclib_fetch').count() == 1
        session.rollback()
        assert session.query(JobRecord).filter_by(kind='lrclib_fetch').count() == 0
        assert session.get(LibraryPublicationRecord, 'publication-attempt-lyric-transaction') is None
        persisted = session.get(LibraryRecord, 'record-lyric-transaction')
        assert persisted is not None and persisted.lyrics_status == 'none'


def test_enqueue_lrclib_fetch_coalesces_active_fetch_per_library_record(tmp_path: Path) -> None:
    # Given: one library record and one unrelated record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-enqueue.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 11, tzinfo=UTC)
    with Session(engine) as session:
        session.add_all(
            (
                LibraryRecord(id='record-active', created_at=now, updated_at=now),
                LibraryRecord(id='record-other', created_at=now, updated_at=now),
            )
        )
        repository = JobRepository(session)

        # When: a fetch is requested again while the first one is queued and then running.
        queued = repository.enqueue_lrclib_fetch('record-active', now)

        # Then: only the active fetch for that record coalesces the request.
        assert queued is not None
        assert (queued.kind, queued.library_record_id, queued.state) == ('lrclib_fetch', 'record-active', 'queued')
        assert repository.enqueue_lrclib_fetch('record-active', now) is None
        queued.state = 'running'
        assert repository.enqueue_lrclib_fetch('record-active', now) is None
        assert repository.enqueue_lrclib_fetch('record-other', now) is not None

        # Then: a finished fetch never blocks the next publication's fetch.
        queued.state = 'completed'
        replacement = repository.enqueue_lrclib_fetch('record-active', now)
        assert replacement is not None and replacement.id != queued.id
        session.commit()
        assert session.query(JobRecord).filter_by(kind='lrclib_fetch').count() == 3


def test_reconcile_with_a_disabled_provider_finalizes_without_queueing_a_fetch(tmp_path: Path) -> None:
    # Given: an exposed replacement for a record with validated lyrics, while the operator has the provider off.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lyric-disabled.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 11, tzinfo=UTC)
    staging = tmp_path / 'staging' / 'attempt-lyric-disabled'
    target = tmp_path / 'media' / 'record-lyric-disabled'
    backup = tmp_path / 'backup' / 'attempt-lyric-disabled'
    staging.mkdir(parents=True)
    _ = (staging / 'audio.flac').write_bytes(b'new-output')
    target.mkdir(parents=True)
    _ = (target / 'audio.flac').write_bytes(b'old-output')
    with Session(engine) as session:
        record, source = _publication_records('record-lyric-disabled', 'source-lyric-disabled', tmp_path, now)
        record.lyrics_status = 'synced'
        record.lyrics_path = 'Fixture Artist/Fixture Album/old.lrc'
        record.lyrics_publication_id = 'publication-old'
        record.lyrics_sha256 = 'd' * 64
        record.lyrics_updated_at = now
        session.add_all((record, source))
        session.add(
            LibraryPublicationRecord(
                id='publication-old',
                library_record_id=record.id,
                source_id=source.id,
                path=str(target / 'audio.flac'),
                format_name='flac',
                content_sha256=sha256(b'old-output').hexdigest(),
                state='current',
                created_at=now,
            )
        )
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'attempt-lyric-disabled',
                record.id,
                source.id,
                append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'New'}, 'test', now).id,
                target,
                'audio.flac',
                staging,
                backup,
                now,
            ),
        )
        mark_staged(session, attempt, now)
        expose_attempt(session, attempt, now)

        # When: the worker recovery pass finalizes it with the live switch reporting a disabled provider.
        reconcile_attempts(session, now, lrclib_enabled=False)

        # Then: the replacement becomes current, but no fetch is queued for lyrics that will never be fetched.
        persisted = session.get(LibraryRecord, record.id)
        previous = session.get(LibraryPublicationRecord, 'publication-old')
        assert persisted is not None and previous is not None
        assert previous.state == 'superseded'
        assert session.get(LibraryPublicationRecord, f'publication-{attempt.id}') is not None
        assert session.query(JobRecord).filter_by(kind='lrclib_fetch').count() == 0

        # ...and: the record is materialized as "no lyrics were requested" rather than pending for a dead fetch.
        assert persisted.lyrics_status == 'none'
        assert (persisted.lyrics_path, persisted.lyrics_publication_id, persisted.lyrics_sha256) == (None, None, None)
        assert persisted.lyrics_updated_at is not None
        assert persisted.lyrics_updated_at.replace(tzinfo=UTC) == now

        # ...and: the finalized attempt was still cleaned up, so recovery leaves no pending workspace behind.
        cleaned = session.get(PublicationAttemptRecord, attempt.id)
        assert cleaned is not None and cleaned.cleaned_at is not None
