from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.library.service import (
    ensure_source_record,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.models import (
    LibraryPublicationRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.normalize.metadata import (
    CanonicalSource,
)
from music_ingest.processing.config import ProcessingConfig
from music_ingest.processing.execution import (
    ExecutionContext,
    ProcessingInfrastructureError,
)
from music_ingest.processing.media_stage import (
    MediaPipelineRequest,
    MediaStagePlan,
    inspect_source_capability,
    process_media,
)
from music_ingest.processing.metadata import (
    fallback_metadata,
    publication_layout,
)
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
    reserve_attempt,
)
from music_ingest.settings import build_runtime_settings

_TAGS_ADAPTER = TypeAdapter(dict[str, str])


@dataclass(frozen=True, slots=True)
class PublicationHandler:
    session: Session
    config: ProcessingConfig
    sources: SourceAccess
    staging: StagingWorkspace
    settings: RuntimeProcessingSettings

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        now = context.now
        source = self.sources.source(claimed)
        record = ensure_source_record(self.session, source, now)
        decision = reevaluate_effective_source_decision(self.session, record.id, now)
        if decision.source_id is not None and decision.source_id != source.id:
            record_event(
                self.session,
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
        source_path = self.sources.owned_source_path(claimed, source, now)
        if source_path is None:
            return
        relative_directory, output_name = publication_layout(tuple(final_tags.items()), source_path.name)
        publication = next((item for item in record.publications if item.state == 'current'), None)
        unsorted_destination = relative_directory == 'Unsorted' and publication is None
        if unsorted_destination:
            output_name = self.staging.allocate_unsorted_filename('.mka')
        target_audio = self.config.media_root / relative_directory / output_name
        if publication is not None and (
            publication.source_id == source.id
            and publication.metadata_revision_id == revision.id
            and Path(publication.path).resolve() == target_audio.resolve()
        ):
            record_event(
                self.session,
                record.id,
                'final_publish_no_change',
                'complete',
                'source, final metadata revision, and output extension already match the current publication',
                now,
                source.id,
            )
            return
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
        destination_release = target_audio.parent
        attempt_token = uuid4().hex
        attempt = reserve_attempt(
            self.session,
            PublicationAttemptRequest(
                f'publication-attempt-{attempt_token}',
                record.id,
                source.id,
                revision.id,
                destination_release,
                output_name,
                self.config.staging_root / 'publication-attempts' / attempt_token,
                self.config.staging_root / 'publication-backups' / attempt_token,
                now,
            ),
        )
        staged_release = Path(attempt.staging_directory)
        staged_release.parent.mkdir(parents=True, exist_ok=True)
        staged_release.mkdir()
        capability = inspect_source_capability(source_path, timeout_seconds=self.settings.timeout_seconds())
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
                self.config.ffmpeg_command,
                self.config.fpcalc_command,
                self.settings.timeout_seconds(),
                build_runtime_settings(self.session, include_genres=True) if metadata is not None else None,
                False,
            )
        )
        mark_staged(self.session, attempt, now)
        if not unsorted_destination:
            acquire_publication_destination_lock(self.session, target_audio)
        expose_attempt(self.session, attempt, now)
        _ = finalize_attempt(self.session, attempt, now)
        cleanup_attempt(attempt)
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self.session, record.id, now)
        release_mbid = final_tags.get('MUSICBRAINZ_ALBUMID', '').strip()
        if release_mbid and self.settings.artwork_enabled():
            _ = JobRepository(self.session).enqueue_release_artwork(release_mbid, now)
        record_event(self.session, record.id, 'final_published', 'complete', None, now, source.id)
