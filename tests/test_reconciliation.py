from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.models import Base, JobRecord, SourceRecord, SourceRootRecord
from music_ingest.reconciliation import reconcile_incoming


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
        initial = reconcile_incoming(session)
        session.commit()

        # When: one file is changed, one is removed, and one is added.
        _ = first.write_bytes(b'first-v2')
        second.unlink()
        third = incoming / 'third.flac'
        _ = third.write_bytes(b'third-v1')
        current = reconcile_incoming(session)
        session.commit()

        _ = first.unlink()
        after_change_removed = reconcile_incoming(session)
        session.commit()

        # Then: the filesystem delta is durable and each new content version is queued once.
        assert initial.added == 2
        assert current.added == 1
        assert current.changed == 1
        assert current.removed == 1
        assert after_change_removed.removed == 1
        assert len(session.scalars(select(SourceRecord)).all()) == 4
        jobs = list(session.scalars(select(JobRecord)).all())
        assert len(jobs) == 6
        assert {job.kind for job in jobs} == {'filesystem_scan', 'selection_refresh'}


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
        _ = reconcile_incoming(session)
        session.commit()
        source = session.scalars(select(SourceRecord)).one()
        job = session.scalars(select(JobRecord)).one()
        source.intake_state = 'quarantined'
        job.state = 'quarantined'
        session.commit()

        # When: the operator scans the unchanged incoming tree after correcting the cause.
        result = reconcile_incoming(session)
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
        result = reconcile_incoming(session)
        session.commit()

        # Then: source provenance stays root-scoped; only FLAC is queued before capability activation.
        sources = list(session.scalars(select(SourceRecord).order_by(SourceRecord.source_path)))
        assert result.added == 7
        assert result.queued_jobs == 2
        assert len(sources) == 7
        assert len(session.scalars(select(JobRecord)).all()) == 2
        duplicate_hash = next(source.sha256 for source in sources if Path(source.source_path).suffix == '.flac')
        assert {source.source_root_id for source in sources if source.sha256 == duplicate_hash} == {'first', 'second'}
        assert all('escape.flac' not in source.source_path for source in sources)
        assert {
            Path(source.source_path).suffix: source.intake_state
            for source in sources
            if Path(source.source_path).suffix != '.flac'
        } == {
            '.m4a': 'unsupported:capability_unavailable',
            '.mp3': 'unsupported:capability_unavailable',
            '.opus': 'unsupported:capability_unavailable',
            '.ogg': 'unsupported:capability_unavailable',
            '.wav': 'unsupported:container_unsupported',
        }

        # When: a FLAC moves inside its root, another root loses a file, and that root is disabled before scanning.
        moved = first_root / 'moved.flac'
        first_flac.rename(moved)
        (second_root / 'two.flac').unlink()
        second = session.get(SourceRootRecord, 'second')
        assert second is not None
        second.enabled = False
        moved_result = reconcile_incoming(session)
        session.commit()

        # Then: only the same enabled root detects the move; disabled-root lifecycle and output state remain untouched.
        second_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'second'))
        assert second_source is not None
        assert moved_result.moved == 1
        assert moved_result.removed == 0
        assert second_source.intake_state != 'disappeared'
        assert second_source.disappeared_at is None


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
        _ = reconcile_incoming(session)
        session.commit()

        # When: only the first root loses its file.
        first_file.unlink()
        result = reconcile_incoming(session)
        session.commit()

        # Then: disappearance is recorded only against the root that was scanned and changed.
        first_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'first'))
        second_source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'second'))
        assert first_source is not None
        assert second_source is not None
        assert result.removed == 1
        assert first_source.intake_state == 'disappeared'
        assert second_source.intake_state != 'disappeared'


def test_reconcile_reactivates_same_inode_after_it_returns_to_its_root(tmp_path: Path) -> None:
    # Given: a scanned FLAC is moved outside its enabled root, then returned without changing its inode.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reappearance.db"}')
    _ = Base.metadata.create_all(engine)
    root_path = tmp_path / 'root'
    root_path.mkdir()
    source_path = root_path / 'track.flac'
    source_path.write_bytes(b'same inode')
    outside_path = tmp_path / 'outside.flac'

    with Session(engine) as session:
        session.add(_root('root', root_path))
        _ = reconcile_incoming(session)
        session.commit()

        # When: reconciliation observes the move out and then the exact source returning.
        source_path.rename(outside_path)
        _ = reconcile_incoming(session)
        outside_path.rename(source_path)
        result = reconcile_incoming(session)
        session.commit()

        # Then: its durable observation returns to available lifecycle state instead of remaining disappeared.
        source = session.scalar(select(SourceRecord).where(SourceRecord.source_root_id == 'root'))
        assert source is not None
        assert result.unchanged == 1
        assert source.intake_state == 'needs_review'
        assert source.disappeared_at is None
