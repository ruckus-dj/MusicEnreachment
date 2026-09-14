from __future__ import annotations

from collections.abc import Set
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Final, final
from uuid import uuid4

from sqlalchemy import Select, and_, false, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session, selectinload

from music_ingest.models.entities import JobAttemptRecord, JobRecord

_LRCLIB_FETCH_KIND: Final = 'lrclib_fetch'
_BLOCKED_INFRASTRUCTURE_STATE: Final = 'blocked_infrastructure'
_FORMER_LRCLIB_MISSING_ADAPTER_REASON: Final = 'ProcessingInfrastructureError: lrclib provider is unavailable'


@dataclass(frozen=True, slots=True)
class ClaimedJob:
    job: JobRecord
    attempt: JobAttemptRecord
    reclaimed_stale: bool = False


@dataclass(frozen=True, slots=True)
class BlockedLrclibRecovery:
    """Outcome of one deliberate recovery batch over the former missing-adapter outage."""

    selected: int
    recovered: int


@final
class JobRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def claim_next(
        self, now: datetime, lease_age: timedelta, allowed_kinds: Set[str] | None = None
    ) -> ClaimedJob | None:
        return self._claim(self._claimable_statement(now, lease_age, allowed_kinds=allowed_kinds), now)

    def claim_next_for_source(
        self, source_id: str, now: datetime, lease_age: timedelta, allowed_kinds: Set[str] | None = None
    ) -> ClaimedJob | None:
        """Claim a ready continuation for one source before returning to the global queue."""
        return self._claim(self._claimable_statement(now, lease_age, source_id, allowed_kinds), now)

    def _claim(self, statement: Select[tuple[JobRecord]], now: datetime) -> ClaimedJob | None:
        candidate = self._session.scalar(statement)
        if candidate is None:
            return None
        stale_attempt = candidate.attempts[-1] if candidate.attempts else None
        reclaimed_stale = False
        if candidate.state == 'running' and stale_attempt is not None:
            stale_attempt.state = 'interrupted'
            stale_attempt.finished_at = now
            reclaimed_stale = True
        candidate.state = 'running'
        attempt = JobAttemptRecord(
            job=candidate,
            attempt_number=len(candidate.attempts) + 1,
            state='running',
            started_at=now,
            finished_at=None,
        )
        self._session.add(attempt)
        self._session.flush()
        return ClaimedJob(candidate, attempt, reclaimed_stale)

    def succeed(self, claimed: ClaimedJob, now: datetime) -> None:
        claimed.attempt.state = 'succeeded'
        claimed.attempt.finished_at = now
        claimed.job.state = 'completed'
        claimed.job.next_attempt_at = None

    def quarantine(self, claimed: ClaimedJob, now: datetime) -> None:
        claimed.attempt.state = 'quarantined'
        claimed.attempt.finished_at = now
        claimed.job.state = 'quarantined'
        claimed.job.next_attempt_at = None

    def retry(
        self, claimed: ClaimedJob, now: datetime, delay: timedelta, max_attempts: int, failure_reason: str
    ) -> None:
        if claimed.attempt.attempt_number >= max_attempts:
            claimed.job.failure_reason = failure_reason
            claimed.attempt.state = 'blocked_infrastructure'
            claimed.attempt.finished_at = now
            claimed.job.state = 'blocked_infrastructure'
            claimed.job.next_attempt_at = None
            return
        claimed.attempt.state = 'retry_wait'
        claimed.attempt.finished_at = now
        claimed.job.state = 'queued'
        claimed.job.next_attempt_at = now + delay

    def requeue_source(self, source_id: str, now: datetime) -> bool:
        """Queue both provider stages from the start of the enrichment pipeline."""
        return self.enqueue(source_id, 'acoustid_analysis', now) is not None

    def requeue_provider(self, source_id: str, provider: str, now: datetime) -> bool:
        """Queue one provider's analysis without re-running the other provider."""
        return self.enqueue(source_id, f'{provider}_analysis', now) is not None

    def enqueue_selection_refresh(self, library_record_id: str, now: datetime) -> JobRecord | None:
        active = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.library_record_id == library_record_id)
            .where(JobRecord.kind == 'selection_refresh')
            .where(JobRecord.state.in_(['queued', 'running']))
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            return None
        try:
            with self._session.begin_nested():
                job = JobRecord(
                    id=f'selection_refresh-{uuid4().hex}',
                    library_record_id=library_record_id,
                    kind='selection_refresh',
                    state='queued',
                    created_at=now,
                )
                self._session.add(job)
                self._session.flush()
                return job
        except IntegrityError:
            active = self._session.scalar(
                select(JobRecord)
                .where(JobRecord.library_record_id == library_record_id)
                .where(JobRecord.kind == 'selection_refresh')
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            if active is None:
                raise
            return None

    def enqueue_lrclib_fetch(self, library_record_id: str, now: datetime) -> JobRecord | None:
        """Queue one synced-lyrics fetch per library record, coalescing an active fetch.

        At most one fetch is active for a record: a queued or running ``lrclib_fetch`` already covering the
        record is reused instead of a second one being queued, so repeatedly superseded publications do not
        accumulate duplicate lyric work. The read and the insert share the caller's transaction, so the job
        becomes durable with the publication that requested it. Database-level uniqueness for this kind is
        deferred to the migration that declares the index, as with the other coalesced kinds.
        """
        active = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.library_record_id == library_record_id)
            .where(JobRecord.kind == _LRCLIB_FETCH_KIND)
            .where(JobRecord.state.in_(['queued', 'running']))
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            return None
        try:
            with self._session.begin_nested():
                job = JobRecord(
                    id=f'lrclib-fetch-{uuid4().hex}',
                    library_record_id=library_record_id,
                    kind=_LRCLIB_FETCH_KIND,
                    state='queued',
                    created_at=now,
                )
                self._session.add(job)
                self._session.flush()
                return job
        except IntegrityError:
            active = self._session.scalar(
                select(JobRecord)
                .where(JobRecord.library_record_id == library_record_id)
                .where(JobRecord.kind == _LRCLIB_FETCH_KIND)
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            if active is None:
                raise
            return None

    def recoverable_blocked_lrclib_fetches_statement(self) -> Select[tuple[JobRecord]]:
        """Blocked lyric fetches attributable to exactly the former missing-adapter outage.

        The pattern is anchored at the end of the recorded reason, so a different terminal failure, a
        wrapped cause, or another provider's outage never matches, and the oldest evidence is offered first.
        """
        return (
            select(JobRecord)
            .where(JobRecord.kind == _LRCLIB_FETCH_KIND)
            .where(JobRecord.state == _BLOCKED_INFRASTRUCTURE_STATE)
            .where(JobRecord.failure_reason.like(f'%{_FORMER_LRCLIB_MISSING_ADAPTER_REASON}'))
            .order_by(JobRecord.created_at, JobRecord.id)
        )

    def count_recoverable_blocked_lrclib_fetches(self) -> int:
        """Count the blocked lyric fetches this deployment can deliberately recover."""
        statement = self.recoverable_blocked_lrclib_fetches_statement()
        return int(self._session.scalar(select(func.count()).select_from(statement.subquery())) or 0)

    def recover_blocked_lrclib_fetches(self, now: datetime, limit: int) -> BlockedLrclibRecovery:
        """Queue fresh lyric fetches for records blocked by the former missing adapter.

        One bounded batch per call: a record is queued at most once, and every recovered fetch is created at
        ``now`` so the batch lands behind the work already waiting instead of starving ordinary processing.
        The blocked jobs stay untouched as evidence — their attempts and failure reason are preserved.
        """
        blocked = list(self._session.scalars(self.recoverable_blocked_lrclib_fetches_statement().limit(limit)).all())
        recovered_records: set[str] = set()
        recovered = 0
        for blocked_job in blocked:
            record_id = blocked_job.library_record_id
            if record_id is None or record_id in recovered_records:
                continue
            recovered_records.add(record_id)
            if self.enqueue_lrclib_fetch(record_id, now) is not None:
                recovered += 1
        return BlockedLrclibRecovery(selected=len(blocked), recovered=recovered)

    def enqueue_folder_release_selection(self, folder_path: str, now: datetime) -> JobRecord | None:
        """Queue one release-selection pass for a source folder."""

        active = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.folder_path == folder_path)
            .where(JobRecord.kind == 'folder_release_selection')
            .where(JobRecord.state.in_(['queued', 'running']))
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            return None
        job = JobRecord(
            id=f'folder-release-selection-{uuid4().hex}',
            folder_path=folder_path,
            kind='folder_release_selection',
            state='queued',
            created_at=now,
        )
        self._session.add(job)
        self._session.flush()
        return job

    def enqueue_reconciliation_scan(self, now: datetime) -> tuple[JobRecord, bool]:
        active = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.kind == 'reconciliation_scan')
            .where(JobRecord.state.in_(['queued', 'running']))
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            return active, False
        try:
            with self._session.begin_nested():
                job = JobRecord(
                    id=f'reconciliation-scan-{uuid4().hex}',
                    kind='reconciliation_scan',
                    state='queued',
                    created_at=now,
                )
                self._session.add(job)
                self._session.flush()
                return job, True
        except IntegrityError:
            active = self._session.scalar(
                select(JobRecord)
                .where(JobRecord.kind == 'reconciliation_scan')
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            if active is None:
                raise
            return active, False

    def enqueue(
        self,
        source_id: str,
        kind: str,
        now: datetime,
        metadata_revision_id: int | None = None,
    ) -> JobRecord | None:
        """Create or coalesce one queued job of a given kind for a source."""
        active = self._session.scalar(
            select(JobRecord)
            .where(
                JobRecord.source_id == source_id,
                JobRecord.kind == kind,
                JobRecord.state.in_(['queued', 'running']),
            )
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            if metadata_revision_id is not None:
                active.metadata_revision_id = metadata_revision_id
            return None
        job = JobRecord(
            id=f'{kind[:63]}-{uuid4().hex}',
            source_id=source_id,
            kind=kind,
            metadata_revision_id=metadata_revision_id,
            state='queued',
            created_at=now,
        )
        self._session.add(job)
        self._session.flush()
        return job

    def enqueue_release_artwork(self, release_mbid: str, now: datetime) -> JobRecord | None:
        """Queue one independent artwork enrichment job for a release MBID."""
        active = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.release_mbid == release_mbid)
            .where(JobRecord.kind == 'artwork_enrichment')
            .where(JobRecord.state.in_(['queued', 'running']))
            .order_by(JobRecord.created_at.desc())
        )
        if active is not None:
            return None
        job = JobRecord(
            id=f'artwork-enrichment-{uuid4().hex}',
            release_mbid=release_mbid,
            kind='artwork_enrichment',
            state='queued',
            created_at=now,
        )
        self._session.add(job)
        self._session.flush()
        return job

    def _claimable_statement(
        self,
        now: datetime,
        lease_age: timedelta,
        source_id: str | None = None,
        allowed_kinds: Set[str] | None = None,
    ) -> Select[tuple[JobRecord]]:
        stale_before = now - lease_age
        statement = (
            select(JobRecord)
            .options(selectinload(JobRecord.attempts))
            .where(
                (
                    (JobRecord.state == 'queued')
                    & ((JobRecord.next_attempt_at.is_(None)) | (JobRecord.next_attempt_at <= now))
                )
                | (
                    (JobRecord.state == 'running')
                    & JobRecord.attempts.any(
                        and_(
                            JobAttemptRecord.state == 'running',
                            JobAttemptRecord.started_at <= stale_before,
                        )
                    )
                )
            )
        )
        if source_id is not None:
            statement = statement.where(JobRecord.source_id == source_id)
        if allowed_kinds is not None:
            if not allowed_kinds:
                return statement.where(false())
            statement = statement.where(JobRecord.kind.in_(allowed_kinds))
        return statement.order_by(JobRecord.created_at, JobRecord.id).limit(1).with_for_update(skip_locked=True)
