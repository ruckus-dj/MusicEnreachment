from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.contracts import ScanResult
from music_ingest.models import Base, JobRecord, LibraryPublicationRecord, SourceRecord, SourceRootRecord
from music_ingest.services.reconciliation import (
    apply_reconciliation_plan,
    load_reconciliation_snapshot,
    plan_reconciliation,
)


def _root(root_id: str, path: Path, *, enabled: bool = True) -> SourceRootRecord:
    now = datetime.now(UTC)
    return SourceRootRecord(
        id=root_id,
        display_name=root_id,
        canonical_path=str(path.resolve()),
        enabled=enabled,
        scan_state='never_scanned',
        created_at=now,
        updated_at=now,
    )


def _reconcile(session: Session) -> ScanResult:
    observed_at = datetime.now(UTC)
    snapshot = load_reconciliation_snapshot(session, observed_at)
    return apply_reconciliation_plan(session, plan_reconciliation(snapshot), observed_at)


def test_plan_reconciliation_runs_from_preloaded_snapshot_without_a_session(tmp_path: Path) -> None:
    # Given: a preloaded snapshot of an enabled root with one incoming file.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "planner.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    _ = root_path.mkdir()
    _ = (root_path / 'track.flac').write_bytes(b'planner source')
    observed_at = datetime.now(UTC)
    with Session(engine) as session:
        session.add(_root('root', root_path))
        snapshot = load_reconciliation_snapshot(session, observed_at)
    # When: the planner receives only immutable snapshots after the session closes.
    plan = plan_reconciliation(snapshot)

    # Then: it returns operations and ScanResult without durable access.
    assert plan.result.added == 1
    assert len(plan.new_sources) == 1


def test_reconcile_incoming_detects_added_changed_and_removed_files(tmp_path: Path) -> None:
    # Given: an incoming folder with two files and an empty durable catalog.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    first = incoming / 'first.flac'
    second = incoming / 'second.flac'
    _ = first.write_bytes(b'first-v1')
    _ = second.write_bytes(b'second-v1')

    with Session(engine) as session:
        session.add(_root('incoming', incoming))
        initial = _reconcile(session)
        session.commit()

        # When: one file is changed, one is removed, and one is added.
        _ = first.write_bytes(b'first-v2')
        second.unlink()
        third = incoming / 'third.flac'
        _ = third.write_bytes(b'third-v1')
        current = _reconcile(session)
        session.commit()

        _ = first.unlink()
        after_change_removed = _reconcile(session)
        session.commit()

        # Then: the filesystem delta is durable and each new content version is queued once.
        assert initial.added == 2
        assert current.added == 1
        assert current.changed == 1
        assert current.removed == 1
        assert after_change_removed.removed == 1
        assert len(session.scalars(select(SourceRecord)).all()) == 2
        jobs = list(session.scalars(select(JobRecord)).all())
        assert len(jobs) == 3
        assert {job.kind for job in jobs} == {'filesystem_scan', 'selection_refresh'}


def test_reconcile_unchanged_source_does_not_duplicate_completed_filesystem_job(tmp_path: Path) -> None:
    # Given: an unchanged source whose deterministic filesystem job has already completed.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation-completed.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    _ = (incoming / 'track.flac').write_bytes(b'unchanged source')

    with Session(engine) as session:
        session.add(_root('incoming', incoming))
        initial = _reconcile(session)
        session.commit()
        job = session.scalars(select(JobRecord).where(JobRecord.kind == 'filesystem_scan')).one()
        job.state = 'completed'
        session.commit()

        # When: reconciliation observes exactly the same source version again.
        repeated = _reconcile(session)
        session.commit()

        # Then: it is a no-op rather than an insert of the same deterministic job ID.
        jobs = list(session.scalars(select(JobRecord).where(JobRecord.kind == 'filesystem_scan')))
        assert initial.queued_jobs == 1
        assert repeated.queued_jobs == 0
        assert len(jobs) == 1
        assert jobs[0].state == 'completed'


def test_reconcile_incoming_requeues_present_quarantined_jobs(tmp_path: Path) -> None:
    # Given: a present source whose previous processing attempt was quarantined.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation-retry.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    source_path = incoming / 'retry.flac'
    _ = source_path.write_bytes(b'retry-source')

    with Session(engine) as session:
        session.add(_root('incoming', incoming))
        _ = _reconcile(session)
        session.commit()
        source = session.scalars(select(SourceRecord)).one()
        job = session.scalars(select(JobRecord)).one()
        source.intake_state = 'quarantined'
        job.state = 'quarantined'
        session.commit()

        # When: the operator scans the unchanged incoming tree after correcting the cause.
        result = _reconcile(session)
        session.commit()

        # Then: the existing job is reactivated without creating a duplicate.
        assert result.queued_jobs == 1
        assert job.state == 'queued'
        assert len(session.scalars(select(JobRecord)).all()) == 1


def test_reconcile_enabled_roots_keeps_provenance_and_inventory_scoped(tmp_path: Path) -> None:
    # Given: two roots with duplicate FLAC bytes, declared formats, an unsupported container, and a link out.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "root-scoped.db"}')
    _ = Base.metadata.create_all(engine)
    first_root = tmp_path / 'first'
    second_root = tmp_path / 'second'
    _ = first_root.mkdir()
    _ = second_root.mkdir()
    outside = tmp_path / 'outside.flac'
    _ = outside.write_bytes(b'outside')
    first_flac = first_root / 'one.flac'
    _ = first_flac.write_bytes(b'duplicate')
    _ = (second_root / 'two.flac').write_bytes(b'duplicate')
    for extension in ('m4a', 'mp3', 'opus', 'ogg'):
        _ = (first_root / f'observed.{extension}').write_bytes(extension.encode())
    _ = (first_root / 'permanent.wav').write_bytes(b'wav')
    (first_root / 'escape.flac').symlink_to(outside)

    with Session(engine) as session:
        session.add_all((_root('first', first_root), _root('second', second_root)))

        # When: configured roots are reconciled.
        result = _reconcile(session)
        session.commit()

        # Then: provenance stays root-scoped and whitelisted audio files are queued.
        sources = list(session.scalars(select(SourceRecord).order_by(SourceRecord.source_path)))
        assert result.added == 7
        assert result.queued_jobs == 7
        assert len(sources) == 7
        assert len(session.scalars(select(JobRecord)).all()) == 7
        assert len({source.sha256 for source in sources if Path(source.source_path).suffix == '.flac'}) == 2
        assert {source.source_root_id for source in sources if Path(source.source_path).suffix == '.flac'} == {
            'first',
            'second',
        }
        assert all('escape.flac' not in source.source_path for source in sources)
        assert {
            Path(source.source_path).suffix: source.intake_state
            for source in sources
            if Path(source.source_path).suffix != '.flac'
        } == {
            '.m4a': 'needs_review',
            '.mp3': 'needs_review',
            '.opus': 'needs_review',
            '.ogg': 'needs_review',
            '.wav': 'needs_review',
        }
        # When: a FLAC moves inside its root, another root loses a file, and that root is disabled before scanning.
        moved = first_root / 'moved.flac'
        _ = first_flac.rename(moved)
        _ = (second_root / 'two.flac').unlink()
        second = session.get(SourceRootRecord, 'second')
        assert second is not None
        second.enabled = False
        moved_result = _reconcile(session)
        session.commit()

        # Then: only the same enabled root detects the move; disabled-root lifecycle and output state remain untouched.
        second_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'second'))
        assert second_source is not None
        assert moved_result.moved == 1
        assert moved_result.removed == 0
        assert second_source.intake_state != 'disappeared'
        assert second_source.disappeared_at is None


def test_reconcile_skips_non_audio_files_before_source_creation(tmp_path: Path) -> None:
    # Given: an input root with obvious non-audio files and a whitelisted audio candidate.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "scan-filter.db"}')
    _ = Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    _ = incoming.mkdir()
    _ = (incoming / 'player.exe').write_bytes(b'executable')
    _ = (incoming / 'library.dll').write_bytes(b'library')
    audio = incoming / 'track.wav'
    _ = audio.write_bytes(b'audio candidate')

    with Session(engine) as session:
        session.add(_root('incoming', incoming))

        # When: the configured root is reconciled.
        result = _reconcile(session)
        session.commit()

        # Then: non-audio files create no source, job, or processing history.
        sources = list(session.scalars(select(SourceRecord)).all())
        jobs = list(session.scalars(select(JobRecord)).all())
        assert result.added == 1
        assert result.queued_jobs == 1
        assert [Path(source.source_path) for source in sources] == [audio]
        assert [job.source_id for job in jobs] == [sources[0].id]


def test_reconcile_disappearance_is_limited_to_the_scanned_root(tmp_path: Path) -> None:
    # Given: two enabled roots whose FLAC observations were previously reconciled.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "deletion-scope.db"}')
    _ = Base.metadata.create_all(engine)
    first_root = tmp_path / 'first'
    second_root = tmp_path / 'second'
    _ = first_root.mkdir()
    _ = second_root.mkdir()
    first_file = first_root / 'first.flac'
    second_file = second_root / 'second.flac'
    _ = first_file.write_bytes(b'first')
    _ = second_file.write_bytes(b'second')

    with Session(engine) as session:
        session.add_all((_root('first', first_root), _root('second', second_root)))
        _ = _reconcile(session)
        session.commit()

        # When: only the first root loses its file.
        first_file.unlink()
        result = _reconcile(session)
        session.commit()

        # Then: only the source in the scanned root is removed.
        first_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'first'))
        second_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'second'))
        assert first_source is None
        assert second_source is not None
        assert result.removed == 1
        assert second_source.intake_state != 'disappeared'


def test_reconcile_creates_a_new_source_when_a_removed_file_returns_to_its_root(tmp_path: Path) -> None:
    # Given: a scanned FLAC is moved outside its enabled root, then returned without changing its inode.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reappearance.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    root_path.mkdir()
    source_path = root_path / 'track.flac'
    _ = source_path.write_bytes(b'same inode')
    outside_path = tmp_path / 'outside.flac'

    with Session(engine) as session:
        session.add(_root('root', root_path))
        _ = _reconcile(session)
        session.commit()

        # When: reconciliation observes the move out and then the exact source returning.
        _ = source_path.rename(outside_path)
        _ = _reconcile(session)
        _ = outside_path.rename(source_path)
        result = _reconcile(session)
        session.commit()

        # Then: it is registered as a new available source after the old unused observation was removed.
        source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'root'))
        assert source is not None
        assert result.added == 1
        assert source.intake_state == 'needs_review'
        assert source.disappeared_at is None


def test_reconcile_removes_disappeared_source_without_a_publication(tmp_path: Path) -> None:
    # Given: a reconciled source that has never produced a managed publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "unpublished-removal.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    _ = root_path.mkdir()
    source_path = root_path / 'track.flac'
    _ = source_path.write_bytes(b'unpublished')

    with Session(engine) as session:
        session.add(_root('root', root_path))
        _ = _reconcile(session)
        session.commit()
        source_id = session.scalars(select(SourceRecord.id)).one()

        # When: the source file disappears from its configured root.
        source_path.unlink()
        result = _reconcile(session)
        session.commit()

        # Then: the missing, unused source observation is removed with its record.
        assert result.removed == 1
        assert session.get(SourceRecord, source_id) is None


def test_reconcile_preserves_disappeared_source_with_a_current_publication(tmp_path: Path) -> None:
    # Given: a reconciled source that owns a managed publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "published-retention.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    _ = root_path.mkdir()
    source_path = root_path / 'track.flac'
    _ = source_path.write_bytes(b'published')
    now = datetime.now(UTC)

    with Session(engine) as session:
        session.add(_root('root', root_path))
        _ = _reconcile(session)
        source = session.scalars(select(SourceRecord)).one()
        assert source.library_record is not None
        session.add(
            LibraryPublicationRecord(
                id='publication-retained',
                library_record=source.library_record,
                source=source,
                path=str(tmp_path / 'media' / 'track.flac'),
                format_name='flac',
                content_sha256='a' * 64,
                state='current',
                created_at=now,
            )
        )
        session.commit()

        # When: the published source file disappears from its configured root.
        source_path.unlink()
        result = _reconcile(session)
        session.commit()

        # Then: the source remains as visibly disappeared provenance for its publication.
        retained = session.get(SourceRecord, source.id)
        assert result.removed == 1
        assert retained is not None
        assert retained.intake_state == 'disappeared'
        assert retained.disappeared_at is not None


def test_reconcile_removes_disappeared_source_with_only_a_superseded_publication(tmp_path: Path) -> None:
    # Given: a reconciled source referenced only by an obsolete publication.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "superseded-removal.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    _ = root_path.mkdir()
    source_path = root_path / 'track.flac'
    _ = source_path.write_bytes(b'superseded')
    now = datetime.now(UTC)

    with Session(engine) as session:
        session.add(_root('root', root_path))
        _ = _reconcile(session)
        source = session.scalars(select(SourceRecord)).one()
        assert source.library_record is not None
        session.add(
            LibraryPublicationRecord(
                id='publication-superseded',
                library_record=source.library_record,
                source=source,
                path=str(tmp_path / 'media' / 'track.flac'),
                format_name='flac',
                content_sha256='a' * 64,
                state='superseded',
                created_at=now,
            )
        )
        session.commit()
        source_id = source.id

        # When: the obsolete source file disappears from its configured root.
        source_path.unlink()
        result = _reconcile(session)
        session.commit()

        # Then: obsolete publication history does not retain the missing source.
        assert result.removed == 1
        assert session.get(SourceRecord, source_id) is None
        assert session.get(LibraryPublicationRecord, 'publication-superseded') is None
