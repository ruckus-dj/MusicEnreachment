from __future__ import annotations

import json
import logging
from collections.abc import Callable
from dataclasses import replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import Final, final
from uuid import uuid4

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.dto import ALLOWED_TAG_KEYS
from music_ingest.enrichment.fingerprints import (
    persist_fingerprint,
)
from music_ingest.inspectors.decoder import DecoderValidationError, validate_decoder
from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.intake.service import SourceId
from music_ingest.library.service import (
    append_metadata_revision,
    ensure_source_record,
    library_record_detail,
    new_library_record,
    record_event,
    record_publication,
    reevaluate_effective_source_decision,
)
from music_ingest.matching.scoring import (
    select_folder_release,
)
from music_ingest.models import (
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    SourceRecord,
    StorageConfigRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.normalize.metadata import (
    CanonicalSource,
    MetadataWriteError,
)
from music_ingest.processing.candidates import (
    _acoustid_recording_mbid as _acoustid_recording_mbid,
)
from music_ingest.processing.candidates import (
    _acoustid_recording_mbids as _acoustid_recording_mbids,
)
from music_ingest.processing.candidates import (
    _acoustid_recording_score as _acoustid_recording_score,
)
from music_ingest.processing.candidates import (
    _aggregate_musicbrainz_results as _aggregate_musicbrainz_results,
)
from music_ingest.processing.candidates import (
    _analyzed_tags as _analyzed_tags,
)
from music_ingest.processing.candidates import (
    _candidate_evidence as _candidate_evidence,
)
from music_ingest.processing.candidates import (
    _candidate_records as _candidate_records,
)
from music_ingest.processing.candidates import (
    _candidate_tags as _candidate_tags,
)
from music_ingest.processing.candidates import (
    _final_tags as _final_tags,
)
from music_ingest.processing.candidates import (
    _folder_selection_root as _folder_selection_root,
)
from music_ingest.processing.candidates import (
    _has_explicit_musicbrainz_identity as _has_explicit_musicbrainz_identity,
)
from music_ingest.processing.candidates import (
    _has_reviewer_decision as _has_reviewer_decision,
)
from music_ingest.processing.candidates import (
    _latest_candidate_run as _latest_candidate_run,
)
from music_ingest.processing.candidates import (
    _matching_request as _matching_request,
)
from music_ingest.processing.candidates import (
    _merge_release_candidate as _merge_release_candidate,
)
from music_ingest.processing.candidates import (
    _release_candidates_for_recording as _release_candidates_for_recording,
)
from music_ingest.processing.candidates import (
    _reviewer_selected_musicbrainz_ids as _reviewer_selected_musicbrainz_ids,
)
from music_ingest.processing.candidates import (
    _score_components_evidence as _score_components_evidence,
)
from music_ingest.processing.candidates import (
    _single_qualified as _single_qualified,
)
from music_ingest.processing.candidates import (
    _single_scored_candidate as _single_scored_candidate,
)
from music_ingest.processing.candidates import (
    _single_scored_recording_candidate as _single_scored_recording_candidate,
)
from music_ingest.processing.candidates import (
    _stored_match_tags as _stored_match_tags,
)
from music_ingest.processing.candidates import (
    _stored_release_candidate as _stored_release_candidate,
)
from music_ingest.processing.candidates import (
    _stored_release_recording_mbid as _stored_release_recording_mbid,
)
from music_ingest.processing.candidates import (
    _stored_release_scores as _stored_release_scores,
)
from music_ingest.processing.candidates import (
    _tag_number as _tag_number,
)
from music_ingest.processing.candidates import (
    _unique_acoustid_album_match as _unique_acoustid_album_match,
)
from music_ingest.processing.candidates import (
    _unique_acoustid_recording_match as _unique_acoustid_recording_match,
)
from music_ingest.processing.candidates import (
    _unique_top_scored as _unique_top_scored,
)
from music_ingest.processing.candidates import (
    musicbrainz_lookup_ids as musicbrainz_lookup_ids,
)
from music_ingest.processing.candidates import (
    select_acoustid_recording_match as select_acoustid_recording_match,
)
from music_ingest.processing.config import ProcessingConfig as ProcessingConfig
from music_ingest.processing.execution import (
    ExecutionContext,
    JobHandler,
    MethodJobHandler,
    ProcessingInfrastructureError,
)
from music_ingest.processing.handlers.analysis import AnalysisHandler
from music_ingest.processing.handlers.artwork import ArtworkHandler
from music_ingest.processing.handlers.reconciliation import ReconciliationHandler
from music_ingest.processing.media_stage import (
    MediaPipelineInfrastructureError,
    MediaPipelineRequest,
    MediaStagePlan,
    PipelineOutputFailure,
    SourceAudioCorruptionError,
    inspect_source_capability,  # noqa: F401 - retained as a compatibility test seam
    plan_media_stage,
    process_media,
)
from music_ingest.processing.metadata import (
    SourceMetadataError,
    fallback_metadata,
    file_hash,
    publication_layout,
)
from music_ingest.processing.remux import RemuxFailure
from music_ingest.processing.support.evidence import SourceEvidence
from music_ingest.processing.support.outcomes import AttemptFinalizer
from music_ingest.processing.support.settings import RuntimeProcessingSettings
from music_ingest.processing.support.sources import SourceAccess
from music_ingest.processing.support.staging import StagingWorkspace
from music_ingest.publication import (
    PublicationAttemptRequest,
    acquire_publication_destination_lock,
    cleanup_attempt,
    expose_attempt,
    finalize_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)
from music_ingest.publication.service import (
    PublicationError,
    PublicationRequest,
    publish_release,  # noqa: F401 - retained as a test seam for legacy publication failure cases
    replace_published_audio,
)
from music_ingest.sanitizers.flac import FlacSanitizationFailure
from music_ingest.settings import build_runtime_settings

LOGGER = logging.getLogger(__name__)
_TAGS_ADAPTER = TypeAdapter(dict[str, str])
_INITIAL_JOB_KINDS: Final = frozenset(
    {'filesystem_scan', 'lidarr_download', 'lidarr_releaseimport', 'lidarr_rename', 'lidarr_albumdelete'}
)


@final
class ProcessingWorker:
    def __init__(self, session: Session, config: ProcessingConfig, *, lease_age: timedelta | None = None) -> None:
        self._session: Session = session
        self._config: ProcessingConfig = config
        self._lease_age: timedelta = lease_age or timedelta(minutes=5)
        self._bind_services()

    def _bind_services(self) -> None:
        self._settings = RuntimeProcessingSettings(self._session, self._config)
        self._outcomes = AttemptFinalizer(self._session, self._config, self._settings)
        self._sources = SourceAccess(self._session, self._outcomes)
        self._evidence = SourceEvidence(self._session, self._config, self._settings)
        self._staging = StagingWorkspace(self._session, self._config)
        self._analysis = AnalysisHandler(self._session, self._sources, self._evidence, self._settings, self._outcomes)

    def run_once(self, *, on_claimed: Callable[[str, str], None] | None = None) -> bool:
        storage = self._session.get(StorageConfigRecord, 1)
        if storage is not None:
            self._config = replace(self._config, media_root=Path(storage.output_root))
        self._bind_services()
        now = datetime.now(UTC)
        reconcile_attempts(self._session, now)
        claimed = JobRepository(self._session).claim_next(now, self._lease_age)
        if claimed is None:
            return False
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
                self._process(claimed, now)
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
        return True

    def _process(self, claimed: ClaimedJob, now: datetime) -> None:
        if claimed.job.source_id is not None:
            source = self._session.get(SourceRecord, claimed.job.source_id)
            if source is not None and source.intake_state == 'replaced':
                claimed.attempt.state = 'succeeded'
                claimed.attempt.finished_at = now
                claimed.job.state = 'superseded'
                claimed.job.next_attempt_at = None
                return
        context = ExecutionContext(self._session, self._config, now)
        handlers: dict[str, JobHandler] = {
            'reconciliation_scan': ReconciliationHandler(),
            'selection_refresh': MethodJobHandler(self._process_selection_refresh),
            'candidate_selection': MethodJobHandler(self._process_candidate_selection),
            'acoustid_analysis': self._analysis,
            'musicbrainz_analysis': self._analysis,
            'folder_release_selection': MethodJobHandler(self._process_folder_release_selection),
            'final_publish': MethodJobHandler(self._process_final_publish),
            'artwork_enrichment': ArtworkHandler(),
        }
        if handler := handlers.get(claimed.job.kind):
            handler.handle(claimed, context)
            return
        if claimed.job.kind in _INITIAL_JOB_KINDS:
            self._process_initial(claimed, now)
            return
        raise ProcessingInfrastructureError(f'unsupported processing job kind: {claimed.job.kind}')

    def _process_selection_refresh(self, claimed: ClaimedJob, now: datetime) -> None:
        record_id = claimed.job.library_record_id
        if record_id is None:
            raise ProcessingInfrastructureError('selection refresh requires a library record target')
        record = self._session.scalar(select(LibraryRecord).where(LibraryRecord.id == record_id).with_for_update())
        if record is None:
            raise ProcessingInfrastructureError('selection refresh library record is missing')
        _ = self._session.scalar(
            select(EffectiveSourceDecisionRecord)
            .where(EffectiveSourceDecisionRecord.library_record_id == record.id)
            .with_for_update()
        )
        _ = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.library_record_id == record.id)
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        decision = reevaluate_effective_source_decision(self._session, record_id, now)
        if decision.source_id is None:
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_eligible_source',
                'complete',
                'no eligible source; current managed output was retained',
                now,
            )
            return
        record = library_record_detail(self._session, record_id)
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == decision.source_id and item.layer == 'final'
            ),
            None,
        )
        if revision is None:
            historical_final = self._session.scalar(
                select(LibraryMetadataRevisionRecord)
                .where(LibraryMetadataRevisionRecord.source_id == decision.source_id)
                .where(LibraryMetadataRevisionRecord.layer == 'final')
                .order_by(LibraryMetadataRevisionRecord.created_at.desc(), LibraryMetadataRevisionRecord.id.desc())
            )
            if historical_final is not None:
                revision = append_metadata_revision(
                    self._session,
                    record.id,
                    decision.source_id,
                    'final',
                    _TAGS_ADAPTER.validate_json(historical_final.tags_json),
                    'reassociation_recovery',
                    now,
                )
        if revision is None:
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_final_revision',
                'needs_review',
                'selected source has no final metadata revision; current managed output was retained',
                now,
                decision.source_id,
            )
            return
        publication = next((item for item in record.publications if item.state == 'current'), None)
        if publication is not None and (
            publication.source_id == decision.source_id
            and publication.metadata_revision_id == revision.id
            and publication.path.casefold().endswith('.mka')
        ):
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_change',
                'complete',
                'selected source and final metadata revision already match the current publication',
                now,
                decision.source_id,
            )
            return
        refresh_publish_job = JobRecord(
            id=f'selection-refresh-publish-{claimed.job.id}',
            source_id=decision.source_id,
            kind='final_publish',
            metadata_revision_id=revision.id,
            state='running',
            created_at=now,
        )
        self._process_final_publish(ClaimedJob(refresh_publish_job, claimed.attempt), now)
        record_event(
            self._session,
            record_id,
            'selection_refresh_selected',
            'complete',
            f'effective source {decision.source_id} selected for refresh',
            now,
            decision.source_id,
        )

    def _process_initial(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._sources.source(claimed)
        source_path = self._sources.owned_source_path(claimed, source, now)
        if source_path is None:
            return
        if self._sources.changed(source, source_path):
            self._outcomes.requeue_changed_source(claimed, source, source_path, now)
            return
        inspection = inspect_media_capability(source_path, timeout_seconds=self._settings.timeout_seconds())
        capability = inspection.capability
        if capability is None:
            detail = inspection.ffprobe.stderr.strip() or inspection.ffprobe.stdout.strip() or 'no ffprobe output'
            self._outcomes.quarantine(
                claimed,
                source,
                (
                    f'input audio stream policy rejected source: ffprobe={inspection.ffprobe.state}; '
                    f'audio_streams={inspection.audio_stream_count}; {detail}'
                ),
                now,
            )
            return
        decoder_evidence = validate_decoder(
            source_path,
            ffmpeg_command=self._config.ffmpeg_command,
            timeout_seconds=self._settings.timeout_seconds(),
        )
        if decoder_evidence is not None:
            self._evidence.record_decoder_evidence(source, decoder_evidence, now)
        source.media_codec = capability.codec.upper()
        technical = inspection.technical
        source.media_bit_depth = None if technical is None else technical.bit_depth
        source.media_sample_rate = None if technical is None else technical.sample_rate
        source.media_channels = None if technical is None else technical.channels
        source.media_bitrate = None if technical is None else technical.bitrate
        cached_fingerprint = self._evidence.cached_fingerprint(source)
        plan = plan_media_stage(source_path)
        tags = plan.source_tags
        self._evidence.capture_observations(source, source_path, tags)
        original_tags = dict(tags)
        metadata = plan.metadata
        record = ensure_source_record(self._session, source, now)
        _ = append_metadata_revision(self._session, record.id, source.id, 'original', original_tags, 'source', now)
        configured_musicbrainz, configured_acoustid, _ = self._settings.configured_providers()
        providers_enabled = configured_musicbrainz is not None or configured_acoustid is not None
        if not _has_explicit_musicbrainz_identity(original_tags):
            if self._evidence.analyze_source(source, source_path) is None:
                return
            source.intake_state = 'present'
            _ = reevaluate_effective_source_decision(self._session, record.id, now)
            if providers_enabled:
                provider_job = 'acoustid_analysis' if configured_acoustid is not None else 'musicbrainz_analysis'
                _ = JobRepository(self._session).enqueue(source.id, provider_job, now)
                record_event(
                    self._session,
                    record.id,
                    'publication_deferred_for_identity',
                    'needs_review',
                    'audio remains unpublished until explicit MusicBrainz recording and release identities are '
                    + 'available',
                    now,
                    source.id,
                )
            else:
                record_event(
                    self._session,
                    record.id,
                    'publication_deferred_for_identity',
                    'needs_review',
                    'audio remains unpublished because explicit MusicBrainz recording and release identities are '
                    + 'missing',
                    now,
                    source.id,
                )
            return
        staged_release = self._staging.staging_directory(claimed.job.id)
        relative_directory, output_name = plan.relative_directory, plan.output_name
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        destination_release = (
            Path(current_publication.path).parent
            if current_publication is not None
            else self._config.media_root / relative_directory
        )
        unsorted_destination = relative_directory == 'Unsorted' and current_publication is None
        if unsorted_destination:
            # Two-stage naming: the durable Unsorted filename is only allocated below, after
            # process_media() has produced output, because allocation must not reserve a name for
            # audio that could still fail to stage. Use a job-scoped hidden name as the pipeline's
            # working output name in the meantime; it is never exposed to callers.
            output_name = f'.{claimed.job.id}.mka'
        pipeline_result = process_media(
            MediaPipelineRequest(
                plan,
                capability,
                staged_release,
                output_name,
                self._config.ffmpeg_command,
                self._config.fpcalc_command,
                self._settings.timeout_seconds(),
                build_runtime_settings(self._session, include_genres=True) if metadata is not None else None,
                cached_fingerprint is None,
            )
        )
        if pipeline_result.fingerprint is not None and cached_fingerprint is None:
            persist_fingerprint(self._session, SourceId(source.id), pipeline_result.fingerprint)
        source.media_codec = capability.codec.upper()
        written_tags = pipeline_result.written_tags
        self._session.flush()
        if unsorted_destination:
            # Staging succeeded; now allocate the real, durable Unsorted filename to replace the
            # hidden working name chosen above.
            output_name = self._staging.allocate_unsorted_filename('.mka')
        target_audio = destination_release / output_name
        path_owner = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.path == str(target_audio.resolve()))
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        if path_owner is not None and path_owner.library_record_id != record.id:
            record_event(
                self._session,
                record.id,
                'publication_path_conflict',
                'needs_review',
                'canonical audio path is already owned by another library record',
                now,
                source.id,
            )
            return
        acquire_publication_destination_lock(self._session, target_audio)
        self._session.refresh(source)
        if source.intake_state == 'replaced':
            record_event(
                self._session,
                record.id,
                'source_generation_superseded',
                'superseded',
                'source was replaced while processing; newer generation is queued',
                now,
                source.id,
            )
            return
        request = PublicationRequest(
            staged_release,
            self._config.staging_root,
            self._config.media_root,
            (source_path,),
            require_canonical_tags=False,
            destination_release=destination_release,
            replace_existing=current_publication is not None,
            destination_audio_name=(
                output_name
                if unsorted_destination
                else Path(current_publication.path).name
                if current_publication is not None
                else None
            ),
            sources=(source,),
        )
        result = replace_published_audio(request, target_audio)
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', dict(written_tags), 'worker', now
        )
        audio_path = result.published_audio
        if audio_path is None:
            audio_path = (
                Path(current_publication.path)
                if current_publication is not None
                else next(result.published_release.glob('*.mka'))
            )
        _ = record_publication(
            self._session,
            record.id,
            source.id,
            audio_path,
            file_hash(audio_path),
            final_revision.id,
            now,
        )
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(
            self._session,
            record.id,
            'publication_ready_for_analysis' if providers_enabled else 'publication_ready_for_review',
            'analyzing' if providers_enabled else 'needs_review',
            'initial final metadata published; provider analysis queued'
            if providers_enabled
            else 'initial final metadata published; no providers configured',
            now,
            source.id,
        )
        if providers_enabled:
            provider_job = 'acoustid_analysis' if configured_acoustid is not None else 'musicbrainz_analysis'
            _ = JobRepository(self._session).enqueue(source.id, provider_job, now)
        if not written_tags:
            # Deliberate: initial publication is not gated on having any tags at all. The audio is
            # already published above so it is reviewable immediately; this event only flags that
            # metadata still needs to be supplied, matching the "keep unresolved matches reviewable
            # rather than blocking" policy rather than a stricter "no publication without tags" rule.
            record_event(
                self._session,
                record.id,
                'metadata_required',
                'analyzing' if providers_enabled else 'needs_review',
                'audio published without usable metadata',
                now,
                source.id,
            )

    def _process_final_publish(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._sources.source(claimed)
        record = ensure_source_record(self._session, source, now)
        decision = reevaluate_effective_source_decision(self._session, record.id, now)
        if decision.source_id is not None and decision.source_id != source.id:
            record_event(
                self._session,
                record.id,
                'final_publish_stale_source',
                'complete',
                'final publication source is no longer the effective source',
                now,
                source.id,
            )
            return
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.layer == 'final'
                and (claimed.job.metadata_revision_id is None or item.id == claimed.job.metadata_revision_id)
            ),
            None,
        )
        if revision is None:
            raise ValueError('final metadata revision is missing')
        final_tags = _TAGS_ADAPTER.validate_json(revision.tags_json)
        source_path = self._sources.owned_source_path(claimed, source, now)
        if source_path is None:
            return
        relative_directory, output_name = publication_layout(tuple(final_tags.items()), source_path.name)
        publication = next((item for item in record.publications if item.state == 'current'), None)
        unsorted_destination = relative_directory == 'Unsorted' and publication is None
        if unsorted_destination:
            output_name = self._staging.allocate_unsorted_filename('.mka')
        target_audio = self._config.media_root / relative_directory / output_name
        if publication is not None and (
            publication.source_id == source.id
            and publication.metadata_revision_id == revision.id
            and Path(publication.path).resolve() == target_audio.resolve()
        ):
            record_event(
                self._session,
                record.id,
                'final_publish_no_change',
                'complete',
                'source, final metadata revision, and output extension already match the current publication',
                now,
                source.id,
            )
            return
        path_owner = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.path == str(target_audio.resolve()))
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        if path_owner is not None and path_owner.library_record_id != record.id:
            record_event(
                self._session,
                record.id,
                'publication_path_conflict',
                'needs_review',
                'canonical audio path is already owned by another library record',
                now,
                source.id,
            )
            return
        destination_release = target_audio.parent
        attempt_token = uuid4().hex
        attempt = reserve_attempt(
            self._session,
            PublicationAttemptRequest(
                f'publication-attempt-{attempt_token}',
                record.id,
                source.id,
                revision.id,
                destination_release,
                output_name,
                self._config.staging_root / 'publication-attempts' / attempt_token,
                self._config.staging_root / 'publication-backups' / attempt_token,
                now,
            ),
        )
        staged_release = Path(attempt.staging_directory)
        staged_release.parent.mkdir(parents=True, exist_ok=True)
        staged_release.mkdir()
        capability = inspect_source_capability(source_path, timeout_seconds=self._settings.timeout_seconds())
        if capability is None:
            raise ProcessingInfrastructureError('source has no declared media capability')
        metadata = fallback_metadata(tuple(final_tags.items()), CanonicalSource.REVIEWED_MANUAL)
        pipeline_plan = MediaStagePlan(
            source_path, tuple(final_tags.items()), metadata, relative_directory, output_name
        )
        _ = process_media(
            MediaPipelineRequest(
                pipeline_plan,
                capability,
                staged_release,
                output_name,
                self._config.ffmpeg_command,
                self._config.fpcalc_command,
                self._settings.timeout_seconds(),
                build_runtime_settings(self._session, include_genres=True) if metadata is not None else None,
                False,
            )
        )
        mark_staged(self._session, attempt, now)
        if not unsorted_destination:
            acquire_publication_destination_lock(self._session, target_audio)
        expose_attempt(self._session, attempt, now)
        _ = finalize_attempt(self._session, attempt, now)
        cleanup_attempt(attempt)
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        release_mbid = final_tags.get('MUSICBRAINZ_ALBUMID', '').strip()
        if release_mbid and self._settings.artwork_enabled():
            _ = JobRepository(self._session).enqueue_release_artwork(release_mbid, now)
        record_event(self._session, record.id, 'final_published', 'complete', None, now, source.id)

    def _process_candidate_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._sources.candidate_selection_source(claimed)
        if source.library_record_id is None:
            record = new_library_record(self._session, now)
            source.library_record = record
            self._session.flush()
        self._enqueue_folder_selection_if_ready(source, claimed.job.id, now)

    def _enqueue_folder_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        folder = _folder_selection_root(source.source_path)
        members = self._sources.folder_members(folder)
        member_ids = tuple(item.id for item in members)
        active_collection = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.source_id.in_(member_ids))
            .where(
                JobRecord.kind.in_(
                    [
                        'filesystem_scan',
                        'acoustid_analysis',
                        'musicbrainz_analysis',
                    ]
                )
            )
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.id != current_job_id)
        )
        if active_collection is not None:
            return
        _ = JobRepository(self._session).enqueue_folder_release_selection(str(folder), now)

    def _process_folder_release_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        folder_path = claimed.job.folder_path
        if folder_path is None:
            raise ProcessingInfrastructureError('folder release selection requires a folder target')
        folder = Path(folder_path)
        members = self._sources.folder_members(folder)
        if not members:
            return
        member_ids = tuple(item.id for item in members)
        if (
            self._session.scalar(
                select(JobRecord)
                .where(JobRecord.source_id.in_(member_ids))
                .where(JobRecord.kind.in_(['filesystem_scan', 'acoustid_analysis', 'musicbrainz_analysis']))
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            is not None
        ):
            return
        groups = tuple(_stored_release_scores(item) for item in members)
        selected_release = select_folder_release(groups, self._settings.confidence_threshold())
        if selected_release is None:
            for source in members:
                if source.library_record_id is not None:
                    record = library_record_detail(self._session, source.library_record_id)
                    record.processing_state = 'needs_review'
                    record.match_state = 'needs_review'
                    record_event(
                        self._session,
                        source.library_record_id,
                        'folder_release_selection_review',
                        'needs_review',
                        'folder candidates have no unique shared release',
                        now,
                        source.id,
                    )
            return
        for source in members:
            if source.library_record_id is None:
                continue
            match = _stored_release_candidate(source, selected_release)
            if match is None:
                record = library_record_detail(self._session, source.library_record_id)
                record.processing_state = 'needs_review'
                record.match_state = 'needs_review'
                record_event(
                    self._session,
                    source.library_record_id,
                    'folder_release_selection_review',
                    'needs_review',
                    'selected folder release is absent from the source candidate run',
                    now,
                    source.id,
                )
                continue
            recording_mbid, match_evidence = match[1].recording_mbid, match[1]
            if recording_mbid is None:
                continue
            associated = RecordingAssociationService(self._session).associate_automatic(
                AutomaticAssociationRequest(
                    source.id,
                    recording_mbid,
                    match_evidence.score or 0.0,
                    self._settings.confidence_threshold(),
                    json.dumps({'recording_mbid': recording_mbid, 'release_mbid': selected_release}, sort_keys=True),
                    now,
                    release_mbid=selected_release,
                )
            )
            if associated is None:
                continue
            record = library_record_detail(self._session, associated.library_record_id)
            analyzed_tags = _stored_match_tags(None, match)
            source_tags = {
                item.tag_name: item.value for item in source.tag_observations if item.tag_name in ALLOWED_TAG_KEYS
            }
            _ = append_metadata_revision(
                self._session, record.id, source.id, 'analyzed', analyzed_tags, 'folder_selection', now
            )
            final_revision = append_metadata_revision(
                self._session,
                record.id,
                source.id,
                'final',
                {**source_tags, **analyzed_tags},
                'folder_selection',
                now,
            )
            _ = JobRepository(self._session).enqueue(source.id, 'final_publish', now, final_revision.id)
            record_event(
                self._session,
                record.id,
                'folder_release_selected',
                'publishing',
                f'folder release {selected_release} selected from complete candidate runs',
                now,
                source.id,
            )
