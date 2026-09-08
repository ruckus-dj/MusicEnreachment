from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.enrichment.fingerprints import (
    persist_fingerprint,
)
from music_ingest.inspectors.decoder import validate_decoder
from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.intake.service import SourceId
from music_ingest.library.service import (
    append_metadata_revision,
    ensure_source_record,
    record_event,
    record_publication,
    reevaluate_effective_source_decision,
)
from music_ingest.models import (
    LibraryPublicationRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.processing.candidates import _has_explicit_musicbrainz_identity
from music_ingest.processing.config import ProcessingConfig
from music_ingest.processing.execution import (
    ExecutionContext,
)
from music_ingest.processing.media_stage import (
    MediaPipelineRequest,
    plan_media_stage,
    process_media,
)
from music_ingest.processing.metadata import (
    file_hash,
)
from music_ingest.processing.support.evidence import SourceEvidence
from music_ingest.processing.support.outcomes import AttemptFinalizer
from music_ingest.processing.support.settings import RuntimeProcessingSettings
from music_ingest.processing.support.sources import SourceAccess
from music_ingest.processing.support.staging import StagingWorkspace
from music_ingest.publication import (
    acquire_publication_destination_lock,
)
from music_ingest.publication.service import (
    PublicationRequest,
    replace_published_audio,
)
from music_ingest.settings import build_runtime_settings


@dataclass(frozen=True, slots=True)
class InitialHandler:
    session: Session
    config: ProcessingConfig
    sources: SourceAccess
    evidence: SourceEvidence
    settings: RuntimeProcessingSettings
    outcomes: AttemptFinalizer
    staging: StagingWorkspace

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        now = context.now
        source = self.sources.source(claimed)
        source_path = self.sources.owned_source_path(claimed, source, now)
        if source_path is None:
            return
        if self.sources.changed(source, source_path):
            self.outcomes.requeue_changed_source(claimed, source, source_path, now)
            return
        inspection = inspect_media_capability(source_path, timeout_seconds=self.settings.timeout_seconds())
        capability = inspection.capability
        if capability is None:
            detail = inspection.ffprobe.stderr.strip() or inspection.ffprobe.stdout.strip() or 'no ffprobe output'
            self.outcomes.quarantine(
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
            ffmpeg_command=self.config.ffmpeg_command,
            timeout_seconds=self.settings.timeout_seconds(),
        )
        if decoder_evidence is not None:
            self.evidence.record_decoder_evidence(source, decoder_evidence, now)
        source.media_codec = capability.codec.upper()
        technical = inspection.technical
        source.media_bit_depth = None if technical is None else technical.bit_depth
        source.media_sample_rate = None if technical is None else technical.sample_rate
        source.media_channels = None if technical is None else technical.channels
        source.media_bitrate = None if technical is None else technical.bitrate
        cached_fingerprint = self.evidence.cached_fingerprint(source)
        plan = plan_media_stage(source_path)
        tags = plan.source_tags
        self.evidence.capture_observations(source, source_path, tags)
        original_tags = dict(tags)
        metadata = plan.metadata
        record = ensure_source_record(self.session, source, now)
        _ = append_metadata_revision(self.session, record.id, source.id, 'original', original_tags, 'source', now)
        configured_musicbrainz, configured_acoustid, _ = self.settings.configured_providers()
        providers_enabled = configured_musicbrainz is not None or configured_acoustid is not None
        if not _has_explicit_musicbrainz_identity(original_tags):
            if self.evidence.analyze_source(source, source_path) is None:
                return
            source.intake_state = 'present'
            _ = reevaluate_effective_source_decision(self.session, record.id, now)
            if providers_enabled:
                provider_job = 'acoustid_analysis' if configured_acoustid is not None else 'musicbrainz_analysis'
                _ = JobRepository(self.session).enqueue(source.id, provider_job, now)
                record_event(
                    self.session,
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
                    self.session,
                    record.id,
                    'publication_deferred_for_identity',
                    'needs_review',
                    'audio remains unpublished because explicit MusicBrainz recording and release identities are '
                    + 'missing',
                    now,
                    source.id,
                )
            return
        staged_release = self.staging.staging_directory(claimed.job.id)
        relative_directory, output_name = plan.relative_directory, plan.output_name
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        destination_release = (
            Path(current_publication.path).parent
            if current_publication is not None
            else self.config.media_root / relative_directory
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
                self.config.ffmpeg_command,
                self.config.fpcalc_command,
                self.settings.timeout_seconds(),
                build_runtime_settings(self.session, include_genres=True) if metadata is not None else None,
                cached_fingerprint is None,
            )
        )
        if pipeline_result.fingerprint is not None and cached_fingerprint is None:
            persist_fingerprint(self.session, SourceId(source.id), pipeline_result.fingerprint)
        source.media_codec = capability.codec.upper()
        written_tags = pipeline_result.written_tags
        self.session.flush()
        if unsorted_destination:
            # Staging succeeded; now allocate the real, durable Unsorted filename to replace the
            # hidden working name chosen above.
            output_name = self.staging.allocate_unsorted_filename('.mka')
        target_audio = destination_release / output_name
        path_owner = self.session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.path == str(target_audio.resolve()))
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        if path_owner is not None and path_owner.library_record_id != record.id:
            record_event(
                self.session,
                record.id,
                'publication_path_conflict',
                'needs_review',
                'canonical audio path is already owned by another library record',
                now,
                source.id,
            )
            return
        acquire_publication_destination_lock(self.session, target_audio)
        self.session.refresh(source)
        if source.intake_state == 'replaced':
            record_event(
                self.session,
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
            self.config.staging_root,
            self.config.media_root,
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
            self.session, record.id, source.id, 'final', dict(written_tags), 'worker', now
        )
        audio_path = result.published_audio
        if audio_path is None:
            audio_path = (
                Path(current_publication.path)
                if current_publication is not None
                else next(result.published_release.glob('*.mka'))
            )
        _ = record_publication(
            self.session,
            record.id,
            source.id,
            audio_path,
            file_hash(audio_path),
            final_revision.id,
            now,
        )
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self.session, record.id, now)
        record_event(
            self.session,
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
            _ = JobRepository(self.session).enqueue(source.id, provider_job, now)
        if not written_tags:
            # Deliberate: initial publication is not gated on having any tags at all. The audio is
            # already published above so it is reviewable immediately; this event only flags that
            # metadata still needs to be supplied, matching the "keep unresolved matches reviewable
            # rather than blocking" policy rather than a stricter "no publication without tags" rule.
            record_event(
                self.session,
                record.id,
                'metadata_required',
                'analyzing' if providers_enabled else 'needs_review',
                'audio published without usable metadata',
                now,
                source.id,
            )
