from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import final

from sqlalchemy import Select, and_, select
from sqlalchemy.orm import Session, selectinload

from music_ingest.persistence.models import JobAttemptRecord, JobRecord


@dataclass(frozen=True, slots=True)
class ClaimedJob:
    job: JobRecord
    attempt: JobAttemptRecord


@final
class JobRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def claim_next(self, now: datetime, lease_age: timedelta) -> ClaimedJob | None:
        candidate = self._session.scalar(self._claimable_statement(now, lease_age))
        if candidate is None:
            return None
        stale_attempt = candidate.attempts[-1] if candidate.attempts else None
        if candidate.state == 'running' and stale_attempt is not None:
            stale_attempt.state = 'interrupted'
            stale_attempt.finished_at = now
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
        return ClaimedJob(candidate, attempt)

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
            self.quarantine(claimed, now)
            return
        claimed.attempt.state = 'retry_wait'
        claimed.attempt.finished_at = now
        claimed.job.state = 'queued'
        claimed.job.next_attempt_at = now + delay

    def requeue_source(self, source_id: str, now: datetime) -> bool:
        """Put a completed source back into the analysis queue without duplicating its job."""
        job = self._session.scalar(select(JobRecord).where(JobRecord.source_id == source_id))
        if job is None:
            self._session.add(
                JobRecord(
                    id=f'provider-retry-{source_id}',
                    source_id=source_id,
                    kind='provider_retry',
                    state='queued',
                    created_at=now,
                )
            )
            return True
        if job.state in {'queued', 'running'}:
            return False
        job.kind = 'provider_retry'
        job.state = 'queued'
        job.next_attempt_at = None
        job.failure_reason = None
        return True

    def _claimable_statement(self, now: datetime, lease_age: timedelta) -> Select[tuple[JobRecord]]:
        stale_before = now - lease_age
        return (
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
            .order_by(JobRecord.created_at, JobRecord.id)
            .limit(1)
            .with_for_update(skip_locked=True)
        )
