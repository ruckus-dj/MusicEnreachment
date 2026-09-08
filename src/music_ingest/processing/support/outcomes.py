from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy.orm import Session

from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.library.service import (
    attach_source,
    ensure_source_record,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.models import (
    LibraryRecord,
    SourceRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.processing.config import ProcessingConfig
from music_ingest.processing.execution import (
    ChangedSource,
    HandlerOutcome,
    ProcessingInfrastructureError,
    QuarantineSource,
)
from music_ingest.processing.support.settings import RuntimeProcessingSettings

LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class AttemptFinalizer:
    session: Session
    config: ProcessingConfig
    settings: RuntimeProcessingSettings

    def apply(self, claimed: ClaimedJob, outcome: HandlerOutcome, now: datetime) -> None:
        if outcome is None:
            return
        source = self.session.get(SourceRecord, outcome.source_id)
        if source is None:
            raise ProcessingInfrastructureError('processing outcome source is missing')
        match outcome:
            case QuarantineSource(reason=reason):
                self.quarantine(claimed, source, reason, now)
            case ChangedSource(path=path):
                self.requeue_changed_source(claimed, source, path, now)

    def retry_claim(
        self,
        claimed: ClaimedJob,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        reason = self.history_reason(claimed, reason, error)
        LOGGER.warning(
            'processing job retry',
            extra={'job_id': claimed.job.id, 'attempt': claimed.attempt.attempt_number},
            exc_info=error,
        )
        repository = JobRepository(self.session)
        repository.retry(
            claimed,
            datetime.now(UTC),
            self.settings.retry_delay(),
            self.settings.max_attempts(),
            reason,
        )
        if claimed.job.library_record_id is not None:
            if self.session.get(LibraryRecord, claimed.job.library_record_id) is None:
                return
            record_event(
                self.session,
                claimed.job.library_record_id,
                'selection_refresh_retry',
                'retrying',
                reason,
                now,
            )
            return
        if claimed.job.source_id is None:
            return
        source = self.session.get(SourceRecord, claimed.job.source_id)
        if source is None:
            return
        record = ensure_source_record(self.session, source, now)
        blocked = claimed.job.state == 'blocked_infrastructure'
        record_event(
            self.session,
            record.id,
            'processing_blocked' if blocked else 'processing_retry',
            'blocked_infrastructure' if blocked else 'retrying',
            reason,
            now,
            source.id,
        )

    def history_reason(self, claimed: ClaimedJob, reason: str, error: BaseException | None = None) -> str:
        error_name = type(error).__name__ if error is not None else 'InputValidationError'
        source_id = claimed.job.source_id or 'record-only'
        cause = ''
        if error is not None and error.__cause__ is not None:
            cause = f' caused_by={type(error.__cause__).__name__}: {error.__cause__}'
        return (
            f'job={claimed.job.id} attempt={claimed.attempt.attempt_number} source={source_id}; '
            f'{error_name}: {reason}{cause}'
        )

    def quarantine(
        self,
        claimed: ClaimedJob,
        source: SourceRecord,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        event_type = 'root_boundary' if reason.startswith('root boundary:') else 'processing_quarantined'
        reason = self.history_reason(claimed, reason, error)
        source.intake_state = 'quarantined'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self.session, source, now)
        _ = reevaluate_effective_source_decision(self.session, record.id, now)
        record_event(self.session, record.id, event_type, 'quarantined', reason, now, source.id)
        JobRepository(self.session).quarantine(claimed, datetime.now(UTC))

    def invalid_audio(
        self,
        claimed: ClaimedJob,
        source: SourceRecord,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        reason = self.history_reason(claimed, reason, error)
        source.intake_state = 'invalid_audio'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self.session, source, now)
        _ = reevaluate_effective_source_decision(self.session, record.id, now)
        record_event(self.session, record.id, 'invalid_audio', 'invalid_audio', reason, now, source.id)
        JobRepository(self.session).quarantine(claimed, datetime.now(UTC))

    def requeue_changed_source(self, claimed: ClaimedJob, source: SourceRecord, path: Path, now: datetime) -> None:
        record = ensure_source_record(self.session, source, now)
        origin = Origin.LIDARR if source.origin == Origin.LIDARR.value else Origin.MANUAL
        replacement = intake_source(
            self.session,
            IntakeRequest(
                source_path=path,
                source_root_id=source.source_root_id,
                origin=origin,
                duration_seconds=source.duration_seconds,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        replacement_source = self.session.get(SourceRecord, replacement.source_id)
        if replacement_source is None:
            raise ProcessingInfrastructureError('changed source replacement was not persisted')
        orphan_record = replacement_source.library_record
        _ = attach_source(self.session, replacement_source.id, record.id, reason='source_replaced', now=now)
        if orphan_record is not None and orphan_record.id != record.id:
            self.session.delete(orphan_record)
        source.intake_state = 'replaced'
        source.replaced_by_source_id = replacement_source.id
        claimed.attempt.state = 'succeeded'
        claimed.attempt.finished_at = datetime.now(UTC)
        claimed.job.state = 'superseded'
        claimed.job.next_attempt_at = None
        _ = JobRepository(self.session).enqueue(replacement.source_id, 'filesystem_scan', now)
