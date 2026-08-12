from __future__ import annotations

import json
import os
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    SourceRecord,
)
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
        assert (target / 'manifest.json').is_file()
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
                'attempt-order', record.id, source.id, None, target, 'audio.flac', staging, backup, now
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
        assert not staging.exists()
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
        assert not staging.exists()
        if checkpoint == 'before-commit':
            assert backup.is_dir()
        else:
            assert not backup.exists()
        if expected_output == b'new-output':
            assert (target / 'manifest.json').is_file()


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
        manifest = json.loads((target / 'manifest.json').read_text(encoding='utf-8'))
        assert (target / 'audio.flac').read_bytes() == b'new-output'
        assert manifest == {
            'attempt_id': 'attempt-atomic',
            'metadata_revision_id': 7,
            'output_sha256': sha256(b'new-output').hexdigest(),
            'source_id': 'source-atomic',
        }
        assert backup.is_dir()
        assert (backup / 'audio.flac').read_bytes() == b'old-output'
        assert not staging.exists()


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
                    None,
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
        _ = os.replace(target, backup)

        # When: restart reconciliation runs before the staging directory is exposed.
        reconcile_attempts(session, now)
        session.commit()

        # Then: the valid old output survives and both transient paths are cleaned.
        assert (target / 'audio.flac').read_bytes() == b'old-output'
        assert not backup.exists()
        assert not staging.exists()


def test_reconcile_after_target_exposure_finalizes_valid_output(tmp_path: Path) -> None:
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
        reconcile_attempts(session, now)
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
