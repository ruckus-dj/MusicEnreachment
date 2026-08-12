from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from stat import S_ISREG

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from music_ingest.dto import ScanResult
from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.library.service import record_event, reevaluate_effective_source_decision
from music_ingest.models import JobRecord, SourceRecord, SourceRootRecord
from music_ingest.models.jobs import JobRepository
from music_ingest.source_boundary import SourceBoundaryError, resolve_regular_file

_SUPPORTED_SUFFIXES = frozenset({'.flac', '.m4a', '.mp3', '.opus', '.ogg'})
_PERMANENTLY_UNSUPPORTED_SUFFIXES = frozenset({'.aac', '.aiff', '.aif', '.wav', '.wma'})


@dataclass(frozen=True, slots=True)
class FileFingerprint:
    path: Path
    root_id: str
    device: int
    inode: int
    size_bytes: int
    sha256: str


def reconcile_incoming(session: Session, incoming_root: Path | None = None) -> ScanResult:
    if incoming_root is not None:
        _ensure_legacy_root(session, incoming_root)
    roots = tuple(session.scalars(select(SourceRootRecord).where(SourceRootRecord.enabled.is_(True))).all())
    sources = list(session.scalars(select(SourceRecord).options(selectinload(SourceRecord.library_record))).all())
    sources_by_path = {(source.source_root_id, _stored_path(source)): source for source in sources}
    sources_by_identity = {(source.source_root_id, *_identity(source)): source for source in sources}
    added = changed = moved = removed = queued_jobs = 0
    scanned_root_ids: set[str] = set()
    scanned_file_count = 0

    for root in roots:
        files = _root_files(root)
        if files is None:
            root.scan_state = 'unavailable'
            root.updated_at = datetime.now(UTC)
            continue
        scanned_root_ids.add(root.id)
        scanned_file_count += len(files)
        root.scan_state = 'scanned'
        root.updated_at = datetime.now(UTC)
        seen_source_ids: set[str] = set()
        current_paths = {file.path for file in files}
        for file in files:
            existing = sources_by_identity.get((root.id, *_identity(file)))
            if existing is not None:
                seen_source_ids.add(existing.id)
                if _stored_path(existing) != file.path:
                    existing.source_path = str(file.path)
                    moved += 1
                if existing.intake_state == 'disappeared':
                    existing.intake_state = _inventory_state(file.path)
                    existing.disappeared_at = None
                    if existing.library_record is not None:
                        existing.library_record.source_state = 'present'
                        existing.library_record.updated_at = datetime.now(UTC)
                if file.path.suffix == '.flac' and _enqueue_job(session, existing.id, datetime.now(UTC)):
                    queued_jobs += 1
                continue
            path_source = sources_by_path.get((root.id, file.path))
            if path_source is None:
                added += 1
            else:
                changed += 1
            intake = intake_source(
                session,
                IntakeRequest(
                    source_path=file.path,
                    source_root_id=root.id,
                    origin=Origin.MANUAL,
                    duration_seconds=None,
                    tag_observations=(),
                    artwork_observations=(),
                    provider_attempts=(),
                    candidates=(),
                    review_decisions=(),
                ),
            )
            seen_source_ids.add(intake.source_id)
            replacement = session.get(SourceRecord, intake.source_id)
            if replacement is None:
                raise RuntimeError('reconciled source was not persisted')
            replacement.intake_state = _inventory_state(file.path)
            if path_source is not None and path_source.library_record_id is not None:
                _replace_source(session, path_source, replacement)
                _ = JobRepository(session).enqueue_selection_refresh(path_source.library_record_id, datetime.now(UTC))
            if file.path.suffix == '.flac' and _enqueue_job(session, intake.source_id, datetime.now(UTC)):
                queued_jobs += 1
        removed += _mark_disappeared(session, sources, root.id, seen_source_ids, current_paths)
    session.flush()
    observed = added + changed + moved
    return ScanResult(
        added=added,
        changed=changed,
        removed=removed,
        moved=moved,
        unchanged=scanned_file_count - observed,
        queued_jobs=queued_jobs,
    )


def _ensure_legacy_root(session: Session, incoming_root: Path) -> None:
    canonical_path = str(incoming_root.resolve(strict=True))
    existing = session.get(SourceRootRecord, 'legacy')
    if existing is not None:
        return
    now = datetime.now(UTC)
    session.add(
        SourceRootRecord(
            id='legacy',
            display_name='legacy',
            canonical_path=canonical_path,
            enabled=True,
            scan_state='never_scanned',
            created_at=now,
            updated_at=now,
        )
    )


def _root_files(root: SourceRootRecord) -> tuple[FileFingerprint, ...] | None:
    raw_root = Path(root.canonical_path)
    try:
        canonical_root = raw_root.resolve(strict=True)
    except FileNotFoundError:
        return None
    if raw_root.is_symlink() or not canonical_root.is_dir():
        return None
    files: list[FileFingerprint] = []
    for directory, directories, names in os.walk(canonical_root, followlinks=False):
        directories[:] = [name for name in directories if not (Path(directory) / name).is_symlink()]
        for name in names:
            candidate = Path(directory) / name
            suffix = candidate.suffix.casefold()
            if suffix not in _SUPPORTED_SUFFIXES | _PERMANENTLY_UNSUPPORTED_SUFFIXES:
                continue
            try:
                files.append(_fingerprint(candidate, canonical_root, root.id))
            except SourceBoundaryError:
                continue
    return tuple(sorted(files, key=lambda item: str(item.path)))


def _fingerprint(path: Path, root: Path, root_id: str) -> FileFingerprint:
    resolved = resolve_regular_file(path, root)
    descriptor = os.open(resolved, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        status = os.fstat(descriptor)
        if not S_ISREG(status.st_mode):
            raise SourceBoundaryError('source path must be a regular file')
        digest = sha256()
        with os.fdopen(descriptor, 'rb', closefd=False) as source_file:
            for chunk in iter(lambda: source_file.read(1024 * 1024), b''):
                digest.update(chunk)
    finally:
        os.close(descriptor)
    return FileFingerprint(resolved, root_id, status.st_dev, status.st_ino, status.st_size, digest.hexdigest())


def _inventory_state(path: Path) -> str:
    if path.suffix.casefold() == '.flac':
        return 'needs_review'
    if path.suffix.casefold() in _SUPPORTED_SUFFIXES:
        return 'unsupported:capability_unavailable'
    return 'unsupported:container_unsupported'


def _replace_source(session: Session, old_source: SourceRecord, replacement: SourceRecord) -> None:
    if old_source.library_record_id is None:
        raise RuntimeError('replaced source must belong to a library record')
    orphan_record = replacement.library_record
    replacement.library_record = old_source.library_record
    old_job = session.scalar(select(JobRecord).where(JobRecord.source_id == old_source.id))
    if old_job is not None and old_job.state in {'queued', 'running'}:
        old_job.state = 'superseded'
        old_job.next_attempt_at = None
    old_source.intake_state = 'replaced'
    old_source.disappeared_at = datetime.now(UTC)
    if orphan_record is not None and orphan_record.id != old_source.library_record_id:
        session.delete(orphan_record)


def _mark_disappeared(
    session: Session,
    sources: list[SourceRecord],
    root_id: str,
    seen_source_ids: set[str],
    current_paths: set[Path],
) -> int:
    removed = 0
    for source in sources:
        if (
            source.source_root_id != root_id
            or source.id in seen_source_ids
            or source.intake_state in {'disappeared', 'replaced'}
        ):
            continue
        if _stored_path(source) in current_paths:
            continue
        source.intake_state = 'disappeared'
        source.disappeared_at = datetime.now(UTC)
        if source.library_record is not None:
            source.library_record.source_state = 'disappeared'
            source.library_record.updated_at = datetime.now(UTC)
            record_event(
                session,
                source.library_record.id,
                'source_disappeared',
                source.library_record.processing_state,
                'filesystem_scan_removed',
                datetime.now(UTC),
                source.id,
            )
            _ = reevaluate_effective_source_decision(session, source.library_record.id, datetime.now(UTC))
            _ = JobRepository(session).enqueue_selection_refresh(source.library_record.id, datetime.now(UTC))
        removed += 1
    return removed


def _stored_path(source: SourceRecord) -> Path:
    return Path(source.source_path).resolve(strict=False)


def _identity(value: FileFingerprint | SourceRecord) -> tuple[int, int, int, str]:
    return value.device, value.inode, value.size_bytes, value.sha256


def _enqueue_job(session: Session, source_id: str, created_at: datetime) -> bool:
    existing = session.scalar(select(JobRecord).where(JobRecord.source_id == source_id))
    if existing is None:
        session.add(
            JobRecord(
                id=f'filesystem-{source_id}',
                source_id=source_id,
                kind='filesystem_scan',
                state='queued',
                created_at=created_at,
            )
        )
        return True
    if existing.state not in {'quarantined', 'blocked_infrastructure'}:
        return False
    existing.state = 'queued'
    existing.next_attempt_at = None
    return True
