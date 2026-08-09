from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import final
from uuid import uuid4

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
        """Queue a distinct provider-analysis job for a source."""
        return self.enqueue(source_id, 'provider_analysis', now) is not None

    def requeue_provider(self, source_id: str, provider: str, now: datetime) -> bool:
        """Queue one provider's analysis without re-running the other provider."""
        return self.enqueue(source_id, f'{provider}_analysis', now) is not None

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
