from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.models import (
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    SourceRecord,
)
from music_ingest.repositories.jobs import ClaimedJob, JobRepository
from music_ingest.services.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.services.candidates import (
    _folder_selection_root,
    _stored_match_tags,
    _stored_release_candidate,
    _stored_release_scores,
)
from music_ingest.services.library.service import (
    append_metadata_revision,
    library_record_detail,
    new_library_record,
    record_event,
    reevaluate_effective_source_decision,
)
from music_ingest.services.matching.scoring import (
    select_folder_release,
)
from music_ingest.services.normalize.source_values import source_values
from music_ingest.workers.execution import (
    ExecutionContext,
    HandlerOutcome,
    ProcessingInfrastructureError,
)
from music_ingest.workers.support.settings import RuntimeProcessingSettings
from music_ingest.workers.support.sources import SourceAccess

_TAGS_ADAPTER = TypeAdapter(dict[str, str])


def refreshed_final_tags(
    record: LibraryRecord, source_id: str, source_tags: dict[str, str], analyzed_tags: dict[str, str]
) -> dict[str, str]:
    manual = next(
        (
            item
            for item in reversed(record.metadata_revisions)
            if item.source_id == source_id and item.layer == 'final' and item.actor == 'manual'
        ),
        None,
    )
    if manual is not None:
        return _TAGS_ADAPTER.validate_json(manual.tags_json)
    return {**source_tags, **analyzed_tags}


@dataclass(frozen=True, slots=True)
class SelectionHandler:
    session: Session
    sources: SourceAccess
    settings: RuntimeProcessingSettings

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome:
        if claimed.job.kind == 'selection_refresh':
            return self.selection_refresh(claimed, context)
        elif claimed.job.kind == 'candidate_selection':
            self.candidate_selection(claimed, context.now)
        elif claimed.job.kind == 'folder_release_selection':
            self.folder_release_selection(claimed, context.now)
        else:
            raise ProcessingInfrastructureError(f'unsupported selection job kind: {claimed.job.kind}')

    def selection_refresh(self, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome:
        now = context.now
        record_id = claimed.job.library_record_id
        if record_id is None:
            raise ProcessingInfrastructureError('selection refresh requires a library record target')
        record = self.session.scalar(select(LibraryRecord).where(LibraryRecord.id == record_id).with_for_update())
        if record is None:
            raise ProcessingInfrastructureError('selection refresh library record is missing')
        _ = self.session.scalar(
            select(EffectiveSourceDecisionRecord)
            .where(EffectiveSourceDecisionRecord.library_record_id == record.id)
            .with_for_update()
        )
        _ = self.session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.library_record_id == record.id)
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        decision = reevaluate_effective_source_decision(self.session, record_id, now)
        if decision.source_id is None:
            record_event(
                self.session,
                record_id,
                'selection_refresh_no_eligible_source',
                'complete',
                'no eligible source; current managed output was retained',
                now,
            )
            return
        record = library_record_detail(self.session, record_id)
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == decision.source_id and item.layer == 'final'
            ),
            None,
        )
        if revision is None:
            historical_final = self.session.scalar(
                select(LibraryMetadataRevisionRecord)
                .where(LibraryMetadataRevisionRecord.source_id == decision.source_id)
                .where(LibraryMetadataRevisionRecord.layer == 'final')
                .order_by(LibraryMetadataRevisionRecord.created_at.desc(), LibraryMetadataRevisionRecord.id.desc())
            )
            if historical_final is not None:
                revision = append_metadata_revision(
                    self.session,
                    record.id,
                    decision.source_id,
                    'final',
                    _TAGS_ADAPTER.validate_json(historical_final.tags_json),
                    'reassociation_recovery',
                    now,
                )
        if revision is None:
            record_event(
                self.session,
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
                self.session,
                record_id,
                'selection_refresh_no_change',
                'complete',
                'selected source and final metadata revision already match the current publication',
                now,
                decision.source_id,
            )
            return
        _ = JobRepository(self.session).enqueue(decision.source_id, 'final_publish', now, revision.id)
        record_event(
            self.session,
            record_id,
            'selection_refresh_selected',
            'complete',
            f'effective source {decision.source_id} selected for refresh',
            now,
            decision.source_id,
        )

    def candidate_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self.sources.candidate_selection_source(claimed)
        if source.library_record_id is None:
            record = new_library_record(self.session, now)
            source.library_record = record
            self.session.flush()
        self.enqueue_folder_selection_if_ready(source, claimed.job.id, now)

    def enqueue_folder_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        folder = _folder_selection_root(source.source_path)
        members = self.sources.folder_members(folder)
        member_ids = tuple(item.id for item in members)
        active_collection = self.session.scalar(
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
        _ = JobRepository(self.session).enqueue_folder_release_selection(str(folder), now)

    def folder_release_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        folder_path = claimed.job.folder_path
        if folder_path is None:
            raise ProcessingInfrastructureError('folder release selection requires a folder target')
        folder = Path(folder_path)
        members = self.sources.folder_members(folder)
        if not members:
            return
        member_ids = tuple(item.id for item in members)
        if (
            self.session.scalar(
                select(JobRecord)
                .where(JobRecord.source_id.in_(member_ids))
                .where(JobRecord.kind.in_(['filesystem_scan', 'acoustid_analysis', 'musicbrainz_analysis']))
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            is not None
        ):
            return
        groups = tuple(_stored_release_scores(item) for item in members)
        selected_release = select_folder_release(groups, self.settings.confidence_threshold())
        if selected_release is None:
            for source in members:
                if source.library_record_id is not None:
                    record = library_record_detail(self.session, source.library_record_id)
                    record.processing_state = 'needs_review'
                    record.match_state = 'needs_review'
                    record_event(
                        self.session,
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
                record = library_record_detail(self.session, source.library_record_id)
                record.processing_state = 'needs_review'
                record.match_state = 'needs_review'
                record_event(
                    self.session,
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
            associated = RecordingAssociationService(self.session).associate_automatic(
                AutomaticAssociationRequest(
                    source.id,
                    recording_mbid,
                    match_evidence.score or 0.0,
                    self.settings.confidence_threshold(),
                    json.dumps({'recording_mbid': recording_mbid, 'release_mbid': selected_release}, sort_keys=True),
                    now,
                    release_mbid=selected_release,
                )
            )
            if associated is None:
                continue
            record = library_record_detail(self.session, associated.library_record_id)
            analyzed_tags = _stored_match_tags(None, match)
            source_tags = source_values(source.tag_observations)
            _ = append_metadata_revision(
                self.session, record.id, source.id, 'analyzed', analyzed_tags, 'folder_selection', now
            )
            _ = append_metadata_revision(
                self.session,
                record.id,
                source.id,
                'final',
                refreshed_final_tags(record, source.id, source_tags, analyzed_tags),
                'folder_selection',
                now,
            )
            # One canonical selection path for folder intake and later refreshes.
            # Select after all folder metadata is durable, not once per source.
            _ = JobRepository(self.session).enqueue_selection_refresh(record.id, now)
            record_event(
                self.session,
                record.id,
                'folder_release_selected',
                'publishing',
                f'folder release {selected_release} selected from complete candidate runs',
                now,
                source.id,
            )
