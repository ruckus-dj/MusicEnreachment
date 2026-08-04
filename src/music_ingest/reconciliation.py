from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from typing import ClassVar

from pydantic import BaseModel, ConfigDict
from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.library.service import attach_source, record_event
from music_ingest.persistence.models import JobRecord, SourceRecord


class ScanResult(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    added: int
    changed: int
    removed: int
    moved: int
    unchanged: int
    queued_jobs: int


@dataclass(frozen=True, slots=True)
class FileFingerprint:
    path: Path
    device: int
    inode: int
    size_bytes: int
    sha256: str


def reconcile_incoming(session: Session, incoming_root: Path) -> ScanResult:
    """Reconcile the durable catalog with every FLAC currently under incoming_root."""
    root = incoming_root.resolve(strict=True)
    files = {_fingerprint(path) for path in root.rglob('*.flac') if path.is_file() and not path.is_symlink()}
    sources = list(
        session.scalars(
            select(SourceRecord).options(
                selectinload(SourceRecord.library_record),
            )
        ).all()
    )
    sources_by_path = {Path(source.source_path).resolve(): source for source in sources}
    sources_by_identity = {_identity(source): source for source in sources}
    seen_source_ids: set[str] = set()
    added = changed = moved = queued_jobs = 0

    for file in sorted(files, key=lambda item: str(item.path)):
        existing = sources_by_identity.get(_identity(file))
        if existing is not None:
            seen_source_ids.add(existing.id)
            if Path(existing.source_path).resolve() != file.path:
                existing.source_path = str(file.path)
                moved += 1
            if _enqueue_job(session, existing.id, datetime.now(UTC)):
                queued_jobs += 1
            continue

        path_source = sources_by_path.get(file.path)
        if path_source is None:
            added += 1
        else:
            changed += 1
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=file.path,
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
        if path_source is not None and path_source.library_record_id is not None:
            replacement = session.get(SourceRecord, intake.source_id)
            if replacement is not None:
                orphan_record = replacement.library_record
                attach_source(
                    session,
                    replacement.id,
                    path_source.library_record_id,
                    reason='source_replaced',
                )
                path_source.intake_state = 'replaced'
                path_source.disappeared_at = datetime.now(UTC)
                if orphan_record is not None and orphan_record.id != path_source.library_record_id:
                    session.delete(orphan_record)
        if _enqueue_job(session, intake.source_id, datetime.now(UTC)):
            queued_jobs += 1

    current_paths = {file.path for file in files}
    removed = 0
    removed_paths: set[Path] = set()
    for source in sources:
        source_path = Path(source.source_path).resolve()
        if source.id in seen_source_ids or source_path in current_paths or source.intake_state == 'disappeared':
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
        if source_path not in removed_paths:
            removed_paths.add(source_path)
            removed += 1

    session.flush()
    return ScanResult(
        added=added,
        changed=changed,
        removed=removed,
        moved=moved,
        unchanged=len(files) - added - changed - moved,
        queued_jobs=queued_jobs,
    )


def _fingerprint(path: Path) -> FileFingerprint:
    resolved = path.resolve(strict=True)
    stat = resolved.stat()
    digest = sha256()
    with resolved.open('rb') as source_file:
        for chunk in iter(lambda: source_file.read(1024 * 1024), b''):
            digest.update(chunk)
    return FileFingerprint(resolved, stat.st_dev, stat.st_ino, stat.st_size, digest.hexdigest())


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
    if existing.state != 'quarantined':
        return False
    existing.state = 'queued'
    existing.next_attempt_at = None
    return True
