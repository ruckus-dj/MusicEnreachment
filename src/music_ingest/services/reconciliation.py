from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from stat import S_ISREG
from uuid import uuid4

from sqlalchemy import bindparam, delete, insert, select, update
from sqlalchemy.orm import Session

from music_ingest.contracts import ScanResult
from music_ingest.models import (
    ArtworkRecord,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    FingerprintRecord,
    JobAttemptRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceAssociationOverrideRecord,
    SourceRecord,
    SourceRecordingAssignmentRecord,
    SourceRootRecord,
    SourceTagRecord,
    WebhookReceiptRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.source_boundary import SourceBoundaryError, resolve_regular_file

_AUDIO_SUFFIXES = frozenset(
    {
        '.aac',
        '.aiff',
        '.alac',
        '.ape',
        '.asf',
        '.dff',
        '.dsf',
        '.flac',
        '.m4a',
        '.mka',
        '.mp3',
        '.mp4',
        '.mpc',
        '.ofr',
        '.ofs',
        '.oga',
        '.ogg',
        '.opus',
        '.spx',
        '.tak',
        '.tta',
        '.wav',
        '.wma',
        '.wv',
    }
)


@dataclass(frozen=True, slots=True)
class FileFingerprint:
    path: Path
    root_id: str
    device: int
    inode: int
    size_bytes: int
    mtime_ns: int
    sha256: str


@dataclass(frozen=True, slots=True)
class RootSnapshot:
    id: str
    canonical_path: str


@dataclass(frozen=True, slots=True)
class SourceSnapshot:
    id: str
    source_root_id: str
    source_path: str
    device: int
    inode: int
    size_bytes: int
    mtime_ns: int
    intake_state: str
    library_record_id: str | None


@dataclass(frozen=True, slots=True)
class JobSnapshot:
    id: str
    source_id: str | None
    library_record_id: str | None
    kind: str
    state: str


@dataclass(frozen=True, slots=True)
class ReconciliationSnapshot:
    roots: tuple[RootSnapshot, ...]
    sources: tuple[SourceSnapshot, ...]
    jobs: tuple[JobSnapshot, ...]
    current_publication_source_ids: frozenset[str]
    active_selection_refresh_record_ids: frozenset[str]
    observed_at: datetime


@dataclass(frozen=True, slots=True)
class NewSource:
    id: str
    library_record_id: str
    root_id: str
    fingerprint: FileFingerprint
    intake_state: str


@dataclass(frozen=True, slots=True)
class ReconciliationPlan:
    result: ScanResult
    scanned_root_ids: tuple[str, ...]
    unavailable_root_ids: tuple[str, ...]
    new_sources: tuple[NewSource, ...]
    moved_paths: tuple[tuple[str, str], ...]
    reappeared_sources: tuple[tuple[str, str], ...]
    replacement_pairs: tuple[tuple[str, str, str], ...]
    disappeared_sources: tuple[tuple[str, str], ...]
    deleted_source_ids: tuple[str, ...]
    deleted_job_ids: tuple[str, ...]
    filesystem_job_inserts: tuple[str, ...]
    filesystem_job_reactivations: tuple[str, ...]
    selection_refresh_record_ids: tuple[str, ...]


def load_reconciliation_snapshot(session: Session, observed_at: datetime) -> ReconciliationSnapshot:
    """Load every durable fact needed by the stat-only reconciliation planner."""
    roots = tuple(
        RootSnapshot(id=root.id, canonical_path=root.canonical_path)
        for root in session.scalars(select(SourceRootRecord).where(SourceRootRecord.enabled.is_(True)))
    )
    sources = tuple(
        SourceSnapshot(
            id=source.id,
            source_root_id=source.source_root_id,
            source_path=source.source_path,
            device=source.device,
            inode=source.inode,
            size_bytes=source.size_bytes,
            mtime_ns=source.mtime_ns,
            intake_state=source.intake_state,
            library_record_id=source.library_record_id,
        )
        for source in session.scalars(select(SourceRecord))
    )
    jobs = tuple(
        JobSnapshot(
            id=job.id,
            source_id=job.source_id,
            library_record_id=job.library_record_id,
            kind=job.kind,
            state=job.state,
        )
        for job in session.scalars(select(JobRecord).order_by(JobRecord.id))
    )
    current_publication_source_ids = frozenset(
        session.scalars(select(LibraryPublicationRecord.source_id).where(LibraryPublicationRecord.state == 'current'))
    )
    active_selection_refresh_record_ids = frozenset(
        record_id
        for record_id in session.scalars(
            select(JobRecord.library_record_id)
            .where(JobRecord.kind == 'selection_refresh')
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.library_record_id.is_not(None))
        )
        if record_id is not None
    )
    return ReconciliationSnapshot(
        roots=roots,
        sources=sources,
        jobs=jobs,
        current_publication_source_ids=current_publication_source_ids,
        active_selection_refresh_record_ids=active_selection_refresh_record_ids,
        observed_at=observed_at,
    )


def plan_reconciliation(snapshot: ReconciliationSnapshot) -> ReconciliationPlan:
    """Compare immutable database and filesystem observations without database access."""
    sources_by_path = {(source.source_root_id, _stored_path(source)): source for source in snapshot.sources}
    sources_by_identity = {(source.source_root_id, *_identity(source)): source for source in snapshot.sources}
    filesystem_job_state_by_source = {
        job.source_id: job.state for job in snapshot.jobs if job.source_id is not None and job.kind == 'filesystem_scan'
    }
    new_sources: list[NewSource] = []
    moved_paths: list[tuple[str, str]] = []
    reappeared_sources: list[tuple[str, str]] = []
    replacement_pairs: list[tuple[str, str, str]] = []
    disappeared_sources: list[tuple[str, str]] = []
    deleted_source_ids: list[str] = []
    job_inserts: list[str] = []
    job_reactivations: list[str] = []
    selection_refresh_record_ids: set[str] = set()
    scanned_root_ids: list[str] = []
    unavailable_root_ids: list[str] = []
    added = changed = moved = removed = queued_jobs = scanned_file_count = 0

    def enqueue(source_id: str) -> None:
        nonlocal queued_jobs
        state = filesystem_job_state_by_source.get(source_id)
        if state is None:
            job_inserts.append(source_id)
            filesystem_job_state_by_source[source_id] = 'queued'
            queued_jobs += 1
        elif state in {'quarantined', 'blocked_infrastructure'}:
            job_reactivations.append(source_id)
            filesystem_job_state_by_source[source_id] = 'queued'
            queued_jobs += 1

    for root in snapshot.roots:
        files = _root_files(root)
        if files is None:
            unavailable_root_ids.append(root.id)
            continue
        scanned_root_ids.append(root.id)
        scanned_file_count += len(files)
        seen_source_ids: set[str] = set()
        current_paths = {file.path for file in files}
        for file in files:
            existing = sources_by_identity.get((root.id, *_identity(file)))
            if existing is not None:
                seen_source_ids.add(existing.id)
                if _stored_path(existing) != file.path:
                    moved_paths.append((existing.id, str(file.path)))
                    moved += 1
                if existing.intake_state == 'disappeared' and existing.library_record_id is not None:
                    reappeared_sources.append((existing.id, existing.library_record_id))
                enqueue(existing.id)
                continue
            path_source = sources_by_path.get((root.id, file.path))
            library_record_id = path_source.library_record_id if path_source is not None else None
            new_source_id = _source_id(root.id, file)
            new_sources.append(
                NewSource(
                    id=new_source_id,
                    library_record_id=library_record_id or f'record-{uuid4().hex}',
                    root_id=root.id,
                    fingerprint=file,
                    intake_state=_inventory_state(file.path),
                )
            )
            seen_source_ids.add(new_source_id)
            if path_source is None:
                added += 1
            else:
                changed += 1
                if library_record_id is not None:
                    replacement_pairs.append((path_source.id, new_source_id, library_record_id))
                    selection_refresh_record_ids.add(library_record_id)
            enqueue(new_source_id)
        for source in snapshot.sources:
            if (
                source.source_root_id != root.id
                or source.id in seen_source_ids
                or source.intake_state in {'disappeared', 'replaced'}
                or _stored_path(source) in current_paths
            ):
                continue
            removed += 1
            if source.id in snapshot.current_publication_source_ids:
                if source.library_record_id is not None:
                    disappeared_sources.append((source.id, source.library_record_id))
                    selection_refresh_record_ids.add(source.library_record_id)
            else:
                deleted_source_ids.append(source.id)
    selection_refresh_record_ids.difference_update(snapshot.active_selection_refresh_record_ids)
    observed = added + changed + moved
    return ReconciliationPlan(
        result=ScanResult(
            added=added,
            changed=changed,
            removed=removed,
            moved=moved,
            unchanged=scanned_file_count - observed,
            queued_jobs=queued_jobs,
        ),
        scanned_root_ids=tuple(scanned_root_ids),
        unavailable_root_ids=tuple(unavailable_root_ids),
        new_sources=tuple(new_sources),
        moved_paths=tuple(moved_paths),
        reappeared_sources=tuple(reappeared_sources),
        replacement_pairs=tuple(replacement_pairs),
        disappeared_sources=tuple(disappeared_sources),
        deleted_source_ids=tuple(deleted_source_ids),
        deleted_job_ids=tuple(job.id for job in snapshot.jobs if job.source_id in deleted_source_ids),
        filesystem_job_inserts=tuple(job_inserts),
        filesystem_job_reactivations=tuple(job_reactivations),
        selection_refresh_record_ids=tuple(sorted(selection_refresh_record_ids)),
    )


def apply_reconciliation_plan(session: Session, plan: ReconciliationPlan, observed_at: datetime) -> ScanResult:
    """Apply a complete reconciliation plan through explicit Core batches without flushing."""
    if plan.scanned_root_ids or plan.unavailable_root_ids:
        root_updates = [
            {'root_id': root_id, 'scan_state': 'scanned', 'updated_at': observed_at}
            for root_id in plan.scanned_root_ids
        ] + [
            {'root_id': root_id, 'scan_state': 'unavailable', 'updated_at': observed_at}
            for root_id in plan.unavailable_root_ids
        ]
        _ = session.connection().execute(
            update(SourceRootRecord)
            .where(SourceRootRecord.id == bindparam('root_id'))
            .execution_options(synchronize_session=False)
            .values(scan_state=bindparam('scan_state'), updated_at=bindparam('updated_at')),
            root_updates,
        )
    replacement_record_ids = {record_id for _, _, record_id in plan.replacement_pairs}
    new_record_ids = {source.library_record_id for source in plan.new_sources} - replacement_record_ids
    if new_record_ids:
        _ = session.execute(
            insert(LibraryRecord),
            [
                {'id': record_id, 'created_at': observed_at, 'updated_at': observed_at}
                for record_id in sorted(new_record_ids)
            ],
        )
    if plan.new_sources:
        _ = session.execute(
            insert(SourceRecord),
            [
                {
                    'id': source.id,
                    'source_path': str(source.fingerprint.path),
                    'device': source.fingerprint.device,
                    'inode': source.fingerprint.inode,
                    'size_bytes': source.fingerprint.size_bytes,
                    'mtime_ns': source.fingerprint.mtime_ns,
                    'sha256': source.fingerprint.sha256,
                    'duration_seconds': None,
                    'origin': 'manual',
                    'intake_state': source.intake_state,
                    'source_root_id': source.root_id,
                    'library_record_id': source.library_record_id,
                }
                for source in plan.new_sources
            ],
        )
    if plan.moved_paths:
        _ = session.connection().execute(
            update(SourceRecord)
            .where(SourceRecord.id == bindparam('source_id'))
            .execution_options(synchronize_session=False)
            .values(source_path=bindparam('source_path')),
            [{'source_id': source_id, 'source_path': path} for source_id, path in plan.moved_paths],
        )
    if plan.reappeared_sources:
        reappeared_source_ids = tuple(source_id for source_id, _ in plan.reappeared_sources)
        reappeared_record_ids = tuple(record_id for _, record_id in plan.reappeared_sources)
        _ = session.execute(
            update(SourceRecord)
            .where(SourceRecord.id.in_(reappeared_source_ids))
            .values(intake_state='needs_review', disappeared_at=None),
        )
        _ = session.execute(
            update(LibraryRecord)
            .where(LibraryRecord.id.in_(reappeared_record_ids))
            .values(source_state='present', updated_at=observed_at),
        )
    if plan.replacement_pairs:
        old_source_ids = tuple(source_id for source_id, _, _ in plan.replacement_pairs)
        _ = session.execute(
            update(SourceRecord)
            .where(SourceRecord.id.in_(old_source_ids))
            .values(intake_state='replaced', disappeared_at=observed_at),
        )
        _ = session.connection().execute(
            update(SourceRecord)
            .where(SourceRecord.id == bindparam('old_source_id'))
            .execution_options(synchronize_session=False)
            .values(replaced_by_source_id=bindparam('replacement_source_id')),
            [
                {'old_source_id': old_source_id, 'replacement_source_id': replacement_source_id}
                for old_source_id, replacement_source_id, _ in plan.replacement_pairs
            ],
        )
        _ = session.execute(
            update(JobRecord)
            .where(JobRecord.source_id.in_(old_source_ids))
            .where(JobRecord.state.in_(['queued', 'running']))
            .values(state='superseded', next_attempt_at=None),
        )
    if plan.disappeared_sources:
        disappeared_source_ids = tuple(source_id for source_id, _ in plan.disappeared_sources)
        _ = session.execute(
            update(SourceRecord)
            .where(SourceRecord.id.in_(disappeared_source_ids))
            .values(intake_state='disappeared', disappeared_at=observed_at),
        )
        _ = session.execute(
            update(LibraryRecord)
            .where(LibraryRecord.id.in_(tuple(record_id for _, record_id in plan.disappeared_sources)))
            .values(source_state='disappeared', updated_at=observed_at),
        )
        _ = session.execute(
            insert(LibraryEventRecord),
            [
                {
                    'library_record_id': library_record_id,
                    'source_id': source_id,
                    'kind': 'source_disappeared',
                    'state': 'queued',
                    'reason': 'filesystem_scan_removed',
                    'details_json': '{}',
                    'created_at': observed_at,
                }
                for source_id, library_record_id in plan.disappeared_sources
            ],
        )
    _delete_unpublished_sources(session, plan.deleted_source_ids, plan.deleted_job_ids)
    if plan.filesystem_job_reactivations:
        _ = session.execute(
            update(JobRecord)
            .where(JobRecord.source_id.in_(plan.filesystem_job_reactivations))
            .where(JobRecord.state.in_(['quarantined', 'blocked_infrastructure']))
            .values(state='queued', next_attempt_at=None),
        )
    filesystem_jobs = [
        {
            'id': f'filesystem-{source_id}',
            'source_id': source_id,
            'kind': 'filesystem_scan',
            'state': 'queued',
            'created_at': observed_at,
        }
        for source_id in plan.filesystem_job_inserts
    ]
    selection_refresh_jobs = [
        {
            'id': f'selection_refresh-{uuid4().hex}',
            'library_record_id': record_id,
            'kind': 'selection_refresh',
            'state': 'queued',
            'created_at': observed_at,
        }
        for record_id in plan.selection_refresh_record_ids
    ]
    if filesystem_jobs:
        _ = session.execute(insert(JobRecord), filesystem_jobs)
    if selection_refresh_jobs:
        _ = session.execute(insert(JobRecord), selection_refresh_jobs)
    return plan.result


def _delete_unpublished_sources(session: Session, source_ids: tuple[str, ...], job_ids: tuple[str, ...]) -> None:
    if not source_ids:
        return
    _ = session.execute(delete(LibraryPublicationRecord).where(LibraryPublicationRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(PublicationAttemptRecord).where(PublicationAttemptRecord.source_id.in_(source_ids)))
    if job_ids:
        _ = session.execute(
            update(WebhookReceiptRecord).where(WebhookReceiptRecord.job_id.in_(job_ids)).values(job_id=None)
        )
        _ = session.execute(delete(JobAttemptRecord).where(JobAttemptRecord.job_id.in_(job_ids)))
    _ = session.execute(delete(JobRecord).where(JobRecord.source_id.in_(source_ids)))
    _ = session.execute(
        delete(SourceAssociationOverrideRecord).where(SourceAssociationOverrideRecord.source_id.in_(source_ids))
    )
    _ = session.execute(
        delete(SourceRecordingAssignmentRecord).where(SourceRecordingAssignmentRecord.source_id.in_(source_ids))
    )
    _ = session.execute(delete(SourceTagRecord).where(SourceTagRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(ArtworkRecord).where(ArtworkRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(ProviderAttemptRecord).where(ProviderAttemptRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(CandidateRecord).where(CandidateRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(ReviewDecisionRecord).where(ReviewDecisionRecord.source_id.in_(source_ids)))
    _ = session.execute(delete(FingerprintRecord).where(FingerprintRecord.source_id.in_(source_ids)))
    _ = session.execute(
        update(EffectiveSourceDecisionRecord)
        .where(EffectiveSourceDecisionRecord.source_id.in_(source_ids))
        .values(source_id=None)
    )
    _ = session.execute(
        update(EffectiveSourceDecisionRecord)
        .where(EffectiveSourceDecisionRecord.baseline_source_id.in_(source_ids))
        .values(baseline_source_id=None)
    )
    _ = session.execute(
        update(LibraryMetadataRevisionRecord)
        .where(LibraryMetadataRevisionRecord.source_id.in_(source_ids))
        .values(source_id=None)
    )
    _ = session.execute(
        update(LibraryEventRecord).where(LibraryEventRecord.source_id.in_(source_ids)).values(source_id=None)
    )
    _ = session.execute(delete(SourceRecord).where(SourceRecord.id.in_(source_ids)))


def mark_disappeared_source(session: Session, source: SourceRecord) -> None:
    """Apply the non-scan disappearance transition used by manual recovery endpoints."""
    has_current_publication = session.scalar(
        select(LibraryPublicationRecord.id)
        .where(LibraryPublicationRecord.source_id == source.id)
        .where(LibraryPublicationRecord.state == 'current')
    )
    if has_current_publication is None:
        job_ids = tuple(session.scalars(select(JobRecord.id).where(JobRecord.source_id == source.id)))
        _delete_unpublished_sources(session, (source.id,), job_ids)
        return
    now = datetime.now(UTC)
    source.intake_state = 'disappeared'
    source.disappeared_at = now
    if source.library_record is None:
        return
    source.library_record.source_state = 'disappeared'
    source.library_record.updated_at = now
    _ = session.execute(
        insert(LibraryEventRecord).values(
            library_record_id=source.library_record.id,
            source_id=source.id,
            kind='source_disappeared',
            state=source.library_record.processing_state,
            reason='filesystem_scan_removed',
            details_json='{}',
            created_at=now,
        )
    )
    _ = JobRepository(session).enqueue_selection_refresh(source.library_record.id, now)


def _root_files(root: RootSnapshot) -> tuple[FileFingerprint, ...] | None:
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
            if candidate.suffix.casefold() not in _AUDIO_SUFFIXES:
                continue
            try:
                files.append(_fingerprint(candidate, canonical_root, root.id))
            except SourceBoundaryError:
                continue
    return tuple(sorted(files, key=lambda item: str(item.path)))


def _fingerprint(path: Path, root: Path, root_id: str) -> FileFingerprint:
    resolved = resolve_regular_file(path, root)
    status = os.stat(resolved, follow_symlinks=False)
    if not S_ISREG(status.st_mode):
        raise SourceBoundaryError('source path must be a regular file')
    fingerprint = sha256(f'{status.st_size}:{status.st_mtime_ns}:{status.st_ino}'.encode()).hexdigest()
    return FileFingerprint(
        resolved, root_id, status.st_dev, status.st_ino, status.st_size, status.st_mtime_ns, fingerprint
    )


def _inventory_state(path: Path) -> str:
    _ = path
    return 'needs_review'


def _stored_path(source: SourceSnapshot) -> Path:
    return Path(source.source_path).resolve(strict=False)


def _identity(value: FileFingerprint | SourceSnapshot) -> tuple[int, int, int, int]:
    return value.device, value.inode, value.size_bytes, value.mtime_ns


def _source_id(root_id: str, fingerprint: FileFingerprint) -> str:
    return sha256(f'{root_id}:{fingerprint.device}:{fingerprint.inode}:{fingerprint.sha256}'.encode()).hexdigest()
