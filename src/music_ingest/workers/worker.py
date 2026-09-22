from __future__ import annotations

from collections.abc import Callable, Set
from dataclasses import replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import Final, final

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.adapters.inspectors.decoder import DecoderValidationError
from music_ingest.adapters.remux import RemuxFailure
from music_ingest.adapters.sanitizers.flac import FlacSanitizationFailure
from music_ingest.models import (
    PublicationAttemptRecord,
    SourceRecord,
    StorageConfigRecord,
)
from music_ingest.repositories.jobs import ClaimedJob, JobRepository
from music_ingest.services.metadata import (
    SourceMetadataError,
)
from music_ingest.services.normalize.metadata import (
    MetadataWriteError,
)
from music_ingest.services.publication import (
    reconcile_attempts,
)
from music_ingest.services.publication.locks import acquire_migration_lock
from music_ingest.services.publication.service import (
    PublicationError,
)
from music_ingest.services.storage_migration import resume_storage_migration
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.execution import (
    ExecutionContext,
    HandlerOutcome,
    JobHandler,
    ProcessingInfrastructureError,
)
from music_ingest.workers.handlers.analysis import AnalysisHandler
from music_ingest.workers.handlers.artwork import ArtworkHandler
from music_ingest.workers.handlers.initial import InitialHandler
from music_ingest.workers.handlers.lrclib import LrclibHandler
from music_ingest.workers.handlers.publication import PublicationHandler
from music_ingest.workers.handlers.reconciliation import ReconciliationHandler
from music_ingest.workers.handlers.selection import SelectionHandler
from music_ingest.workers.media_stage import (
    MediaPipelineInfrastructureError,
    PipelineOutputFailure,
    SourceAudioCorruptionError,
)
from music_ingest.workers.support.evidence import SourceEvidence
from music_ingest.workers.support.outcomes import AttemptFinalizer
from music_ingest.workers.support.settings import RuntimeProcessingSettings
from music_ingest.workers.support.sources import SourceAccess
from music_ingest.workers.support.staging import StagingWorkspace

_INITIAL_JOB_KINDS: Final = frozenset({'filesystem_scan'})


@final
class ProcessingWorker:
    """Own transaction checkpoints: prepare in a savepoint, commit, then publish durably."""

    def __init__(self, session: Session, config: ProcessingConfig, *, lease_age: timedelta | None = None) -> None:
        self._session: Session = session
        self._config: ProcessingConfig = config
        self._lease_age: timedelta = lease_age or timedelta(minutes=5)
        self._bind_services()

    def _lrclib_enabled(self) -> bool:
        """Whether the live runtime wiring still allows lyric fetches.

        The adapter is the runtime's single authority: a settings-backed adapter reads the persisted provider switch,
        so one saved from the UI is honored on the next finalize without a restart. An unwired adapter is an
        incomplete deployment rather than a disabled provider, so its fetch stays queued for the handler to retry as
        infrastructure instead of being settled as if the operator had switched lyrics off.
        """
        adapter = self._config.lrclib_adapter
        return adapter is None or adapter.enabled

    def _bind_services(self) -> None:
        self._settings = RuntimeProcessingSettings(self._session, self._config)
        self._outcomes = AttemptFinalizer(self._session, self._config, self._settings)
        self._sources = SourceAccess(self._session)
        self._evidence = SourceEvidence(self._session, self._config, self._settings)
        self._staging = StagingWorkspace(self._session, self._config)
        self._publication = PublicationHandler(
            self._session, self._config, self._sources, self._staging, self._settings
        )
        self._initial = InitialHandler(
            self._session, self._config, self._sources, self._evidence, self._settings, self._staging
        )
        self._selection = SelectionHandler(self._session, self._sources, self._settings)
        self._analysis = AnalysisHandler(self._session, self._sources, self._evidence, self._settings)
        self._handlers: dict[str, JobHandler] = {
            'reconciliation_scan': ReconciliationHandler(),
            'selection_refresh': self._selection,
            'candidate_selection': self._selection,
            'acoustid_analysis': self._analysis,
            'musicbrainz_analysis': self._analysis,
            'folder_release_selection': self._selection,
            'final_publish': self._publication,
            'artwork_enrichment': ArtworkHandler(),
            'lrclib_fetch': LrclibHandler(),
            **dict.fromkeys(_INITIAL_JOB_KINDS, self._initial),
        }

    def run_once(
        self,
        *,
        on_claimed: Callable[[str, str], None] | None = None,
        allowed_kinds: Set[str] | None = None,
    ) -> bool:
        now = datetime.now(UTC)
        if allowed_kinds is None:
            # Standalone callers retain recovery; runtime pools use their dedicated maintenance loop.
            self.maintain_storage()
            self._session.commit()
        acquire_migration_lock(self._session)
        storage = self._session.get(StorageConfigRecord, 1)
        if storage is not None:
            self._session.refresh(storage)
            if storage.state != 'ready':
                return False
            self._config = replace(self._config, media_root=Path(storage.output_root))
        self._bind_services()
        claimed = JobRepository(self._session).claim_next(now, self._lease_age, allowed_kinds)
        if claimed is None:
            return False
        # Freeze scalar settings before any handler or callback can change them.
        _ = self._settings.values
        if on_claimed is not None:
            on_claimed(claimed.job.id, claimed.job.kind)
        if (
            claimed.reclaimed_stale
            and claimed.job.kind in {'acoustid_analysis', 'musicbrainz_analysis'}
            and claimed.attempt.attempt_number > self._settings.max_attempts()
        ):
            self._outcomes.retry_claim(claimed, 'provider job exceeded max attempts after stale worker lease', now)
            return True
        # Exception -> action table. Every processing failure lands in exactly one of these three
        # buckets:
        #   - retry: transient/infrastructure failures, or errors whose cause is not yet diagnosed.
        #     The job is retried up to its attempt limit.
        #   - invalid_audio: the source audio itself is corrupt (decoder evidence is captured first).
        #   - quarantine: the source's audio decodes but fails validation or lacks usable metadata
        #     (decoder evidence is captured first when available).
        try:
            with self._session.begin_nested():
                outcome = self._process(claimed, now)
                self._outcomes.apply(claimed, outcome, now)
        except (
            MetadataWriteError,
            ProcessingInfrastructureError,
            PipelineOutputFailure,
            MediaPipelineInfrastructureError,
            RemuxFailure,
            OSError,
            PublicationError,
            FlacSanitizationFailure,
            CalledProcessError,
            TimeoutExpired,
            ValueError,
        ) as error:
            self._outcomes.retry_claim(claimed, str(error), now, error)
        except SourceAudioCorruptionError as error:
            source = self._sources.source(claimed)
            self._evidence.record_decoder_evidence(source, error.source_evidence, now)
            self._outcomes.invalid_audio(claimed, source, str(error), now, error)
        except DecoderValidationError as error:
            source = self._sources.source(claimed)
            if error.evidence is not None:
                self._evidence.record_decoder_evidence(source, error.evidence, now)
            self._outcomes.quarantine(claimed, source, str(error), now, error)
        except SourceMetadataError as error:
            source = self._sources.source(claimed)
            self._outcomes.quarantine(claimed, source, str(error), now, error)
        except Exception as error:  # noqa: BLE001
            self._outcomes.retry_claim(claimed, f'unexpected processing error: {error}', now, error)
        finally:
            self._staging.discard_staging(claimed.job.id)
            if claimed.attempt.state == 'running':
                JobRepository(self._session).succeed(claimed, datetime.now(UTC))
        self._session.commit()
        if allowed_kinds is None:
            self.maintain_storage()
        elif claimed.job.source_id is not None and claimed.job.kind in _INITIAL_JOB_KINDS | {'final_publish'}:
            reconcile_attempts(
                self._session,
                datetime.now(UTC),
                lrclib_enabled=self._lrclib_enabled(),
                source_id=claimed.job.source_id,
            )
        return True

    def maintain_storage(self) -> None:
        """Recovery has its own runtime capacity, never runs before unrelated pool jobs."""
        storage = self._session.get(StorageConfigRecord, 1, populate_existing=True)
        if storage is not None and storage.state == 'migrating':
            resume_storage_migration(self._session)
            return
        pending = self._session.scalar(
            select(PublicationAttemptRecord.id).where(PublicationAttemptRecord.cleaned_at.is_(None)).limit(1)
        )
        if pending is not None:
            reconcile_attempts(self._session, datetime.now(UTC), lrclib_enabled=self._lrclib_enabled())

    def _process(self, claimed: ClaimedJob, now: datetime) -> HandlerOutcome:
        if claimed.job.source_id is not None:
            source = self._session.get(SourceRecord, claimed.job.source_id)
            if source is not None and (
                source.intake_state == 'replaced'
                or (
                    claimed.job.source_metadata_revision is not None
                    and claimed.job.source_metadata_revision != source.source_metadata_revision
                )
            ):
                claimed.attempt.state = 'succeeded'
                claimed.attempt.finished_at = now
                claimed.job.state = 'superseded'
                claimed.job.next_attempt_at = None
                return
        context = ExecutionContext(self._session, self._config, now, self._settings)
        if handler := self._handlers.get(claimed.job.kind):
            return handler.handle(claimed, context)
        raise ProcessingInfrastructureError(f'unsupported processing job kind: {claimed.job.kind}')
