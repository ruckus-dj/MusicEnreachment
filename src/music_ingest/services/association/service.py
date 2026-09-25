from __future__ import annotations

import json
from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Final, final, override

from pydantic import TypeAdapter
from sqlalchemy import select, tuple_
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session, raiseload, selectinload

from music_ingest.models import (
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceAssociationOverrideRecord,
    SourceRecord,
    SourceRecordingAssignmentRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import (
    append_metadata_revision,
    new_library_record,
    reevaluate_effective_source_decision,
    reevaluate_effective_source_decisions,
)
from music_ingest.services.matching.providers import (
    Ambiguous,
    FixtureCase,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    MusicBrainzProvider,
)

_TAGS: Final = TypeAdapter(dict[str, str])
_MANUAL_ASSOCIATION_RATIONALE: Final = 'MusicBrainz recording selected manually'


@dataclass(frozen=True, slots=True)
class AutomaticAssociationRequest:
    source_id: str
    recording_mbid: str
    score: float
    confidence_threshold: float
    evidence_json: str
    now: datetime
    release_mbid: str | None = None


@dataclass(frozen=True, slots=True)
class ManualAssociationRequest:
    source_id: str
    recording_mbid: str
    now: datetime
    release_mbid: str | None = None
    actor: str = 'manual'
    track_mbid: str | None = None


@dataclass(frozen=True, slots=True)
class AssociationResult:
    library_record_id: str
    moved_from_record_id: str
    publication_refresh_queued: bool = False


@dataclass(frozen=True, slots=True)
class RecordingAssociationUnavailable(Exception):
    recording_mbid: str

    @override
    def __str__(self) -> str:
        return f'MusicBrainz recording {self.recording_mbid} is unavailable for verification'


@final
class RecordingAssociationService:
    def __init__(self, session: Session, musicbrainz: MusicBrainzProvider | None = None) -> None:
        self._session = session
        self._musicbrainz = musicbrainz

    def associate_automatic(self, request: AutomaticAssociationRequest) -> AssociationResult | None:
        release_mbid = request.release_mbid
        if request.score < request.confidence_threshold or release_mbid is None:
            self._record_review(
                request.source_id,
                'automatic recording score is below the configured confidence threshold',
                request.now,
            )
            return None
        return self._associate(
            request.source_id,
            request.recording_mbid,
            'automatic',
            None,
            request.evidence_json,
            request.now,
            release_mbid,
        )

    def associate_automatic_batch(
        self, requests: Sequence[AutomaticAssociationRequest]
    ) -> tuple[AssociationResult | None, ...]:
        request_items = tuple(enumerate(requests))
        if not request_items:
            return ()
        sources = tuple(
            self._session.scalars(
                select(SourceRecord)
                .where(SourceRecord.id.in_(tuple(sorted({request.source_id for _, request in request_items}))))
                .order_by(SourceRecord.id)
                .options(raiseload('*'))
                .execution_options(populate_existing=True)
                .with_for_update()
            ).all()
        )
        sources_by_id = {source.id: source for source in sources}
        for _, request in request_items:
            if request.source_id not in sources_by_id:
                raise LookupError(request.source_id)

        association_items: list[tuple[int, AutomaticAssociationRequest]] = []
        for item in request_items:
            _, request = item
            if request.score >= request.confidence_threshold and request.release_mbid is not None:
                association_items.append(item)

        target_pairs = tuple(
            sorted(
                {
                    (request.recording_mbid, request.release_mbid)
                    for _, request in association_items
                    if request.release_mbid is not None
                }
            )
        )
        target_times = {
            pair: min(
                request.now
                for _, request in association_items
                if (request.recording_mbid, request.release_mbid) == pair
            )
            for pair in target_pairs
        }
        targets_by_pair = {
            (target.musicbrainz_recording_id, target.musicbrainz_release_id): target
            for target in self._session.scalars(
                select(LibraryRecord)
                .where(
                    tuple_(LibraryRecord.musicbrainz_recording_id, LibraryRecord.musicbrainz_release_id).in_(
                        target_pairs
                    )
                )
                .options(raiseload('*'))
                .execution_options(populate_existing=True)
            ).all()
        }
        for recording_mbid, release_mbid in target_pairs:
            if (recording_mbid, release_mbid) in targets_by_pair:
                continue
            try:
                with self._session.begin_nested():
                    target = new_library_record(self._session, target_times[(recording_mbid, release_mbid)])
                    target.musicbrainz_recording_id = recording_mbid
                    target.musicbrainz_release_id = release_mbid
                    target.match_state = 'matched'
                    self._session.flush()
            except IntegrityError:
                target = self._session.scalar(
                    select(LibraryRecord)
                    .where(LibraryRecord.musicbrainz_recording_id == recording_mbid)
                    .where(LibraryRecord.musicbrainz_release_id == release_mbid)
                    .options(raiseload('*'))
                    .execution_options(populate_existing=True)
                )
                if target is None:
                    raise
            targets_by_pair[(recording_mbid, release_mbid)] = target

        affected_record_ids: set[str] = set()
        review_records: list[tuple[int, AutomaticAssociationRequest, SourceRecord, str]] = []
        unchanged_records: list[tuple[int, AutomaticAssociationRequest, LibraryRecord]] = []
        associations: list[tuple[int, AutomaticAssociationRequest, SourceRecord, LibraryRecord, str]] = []
        for index, request in sorted(request_items, key=lambda item: item[1].source_id):
            source = sources_by_id[request.source_id]
            previous_record_id = source.library_record_id
            if previous_record_id is None:
                raise LookupError(source.id)
            if request.score < request.confidence_threshold or request.release_mbid is None:
                review_records.append((index, request, source, previous_record_id))
                affected_record_ids.add(previous_record_id)
                continue
            target = targets_by_pair[(request.recording_mbid, request.release_mbid)]
            if previous_record_id == target.id:
                affected_record_ids.add(target.id)
                unchanged_records.append((index, request, target))
                continue
            associations.append((index, request, source, target, previous_record_id))
            affected_record_ids.update((previous_record_id, target.id))

        locked_records = {
            record.id: record
            for record in self._session.scalars(
                select(LibraryRecord)
                .where(LibraryRecord.id.in_(tuple(sorted(affected_record_ids))))
                .order_by(LibraryRecord.id)
                .options(raiseload('*'), selectinload(LibraryRecord.metadata_revisions))
                .execution_options(populate_existing=True)
                .with_for_update()
            ).all()
        }
        results: list[AssociationResult | None] = [None] * len(request_items)
        postprocess_record_ids: set[str] = set()
        postprocess_times: dict[str, datetime] = {}
        for index, request, target in unchanged_records:
            results[index] = AssociationResult(target.id, target.id)
            postprocess_record_ids.add(target.id)
            postprocess_times[target.id] = request.now
        for index, request, source, previous_record_id in review_records:
            record = locked_records[previous_record_id]
            record.processing_state = 'needs_review'
            record.match_state = 'needs_review'
            record.updated_at = request.now
            self._session.add(
                ReviewDecisionRecord(
                    source_id=source.id,
                    state='association_review_required',
                    rationale='automatic recording score is below the configured confidence threshold',
                )
            )
            self._session.add(
                LibraryEventRecord(
                    library_record_id=record.id,
                    source_id=source.id,
                    kind='recording_association_review_required',
                    state='needs_review',
                    reason='automatic recording score is below the configured confidence threshold',
                    details_json='{}',
                    created_at=request.now,
                )
            )
            results[index] = None

        final_revisions = {
            record.id: max(
                (revision.revision for revision in record.metadata_revisions if revision.layer == 'final'), default=0
            )
            for record in locked_records.values()
        }
        for index, request, source, target, previous_record_id in associations:
            previous_record = locked_records[previous_record_id]
            locked_target = locked_records[target.id]
            previous_final = next(
                (
                    revision
                    for revision in reversed(previous_record.metadata_revisions)
                    if revision.source_id == source.id and revision.layer == 'final'
                ),
                None,
            )
            provider_final = next(
                (
                    revision
                    for revision in reversed(locked_target.metadata_revisions)
                    if revision.layer == 'final' and revision.actor == 'provider'
                ),
                None,
            )
            source.library_record_id = locked_target.id
            source.disappeared_at = None
            locked_target.source_state = 'present'
            locked_target.updated_at = request.now
            inherited_final = provider_final or previous_final
            if inherited_final is not None:
                final_revisions[locked_target.id] += 1
                locked_target.metadata_state = 'final'
                self._session.add(
                    LibraryMetadataRevisionRecord(
                        library_record_id=locked_target.id,
                        source_id=source.id,
                        layer='final',
                        revision=final_revisions[locked_target.id],
                        tags_json=json.dumps(
                            _TAGS.validate_json(inherited_final.tags_json), ensure_ascii=False, sort_keys=True
                        ),
                        actor='reassociation',
                        created_at=request.now,
                    )
                )
            self._session.add(
                SourceRecordingAssignmentRecord(
                    source_id=source.id,
                    library_record_id=locked_target.id,
                    state='automatic_verified',
                    actor='automatic',
                    rationale=None,
                    evidence_json=json.dumps(
                        {
                            'after_record_id': locked_target.id,
                            'before_record_id': previous_record_id,
                            'evidence': request.evidence_json,
                        },
                        sort_keys=True,
                    ),
                    created_at=request.now,
                )
            )
            for record in (previous_record, locked_target):
                self._session.add(
                    LibraryEventRecord(
                        library_record_id=record.id,
                        source_id=source.id,
                        kind='source_recording_reassigned',
                        state='matched' if record.id == locked_target.id else 'needs_review',
                        reason='confidence-qualified provider recording evidence',
                        details_json=json.dumps(
                            {
                                'actor': 'automatic',
                                'after_record_id': locked_target.id,
                                'before_record_id': previous_record_id,
                                'recording_mbid': request.recording_mbid,
                                'release_mbid': request.release_mbid,
                            },
                            sort_keys=True,
                        ),
                        created_at=request.now,
                    )
                )
            results[index] = AssociationResult(locked_target.id, previous_record_id)
            postprocess_record_ids.update((previous_record_id, locked_target.id))
            postprocess_times[previous_record_id] = request.now
            postprocess_times[locked_target.id] = request.now

        self._session.flush()
        _ = reevaluate_effective_source_decisions(
            self._session,
            {record_id: postprocess_times[record_id] for record_id in sorted(postprocess_record_ids)},
        )
        refresh_record_ids = tuple(
            self._session.scalars(
                select(LibraryRecord.id)
                .where(LibraryRecord.id.in_(tuple(sorted(postprocess_record_ids))))
                .where(
                    LibraryRecord.sources.any()
                    | LibraryRecord.publications.any(LibraryPublicationRecord.state == 'current')
                    | LibraryRecord.publication_attempts.any(
                        PublicationAttemptRecord.state.in_(['reserved', 'staged', 'prepared', 'exposed'])
                    )
                )
                .order_by(LibraryRecord.id)
            ).all()
        )
        _ = JobRepository(self._session).enqueue_selection_refreshes(
            {record_id: postprocess_times[record_id] for record_id in refresh_record_ids}
        )
        for index, request in request_items:
            if (
                results[index] is None
                and request.release_mbid is not None
                and request.score >= request.confidence_threshold
            ):
                target = targets_by_pair[(request.recording_mbid, request.release_mbid)]
                results[index] = AssociationResult(target.id, target.id)
        return tuple(results)

    def associate_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        self._verify_recording(request.recording_mbid, request.now)
        return self._associate_manual(request)

    def associate_verified_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        """Reassign using already persisted provider evidence selected by a reviewer."""
        return self._associate_manual(request)

    def _associate_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        source = self._source(request.source_id)
        override = source.association_override
        evidence = json.dumps(
            {'recording_mbid': request.recording_mbid, 'track_mbid': request.track_mbid},
            sort_keys=True,
        )
        if request.release_mbid is None:
            if source.library_record_id is None:
                raise LookupError(source.id)
            self._record_review(source.id, 'a MusicBrainz release must be selected with the recording', request.now)
            result = AssociationResult(source.library_record_id, source.library_record_id)
        else:
            result = self._associate(
                request.source_id,
                request.recording_mbid,
                request.actor,
                _MANUAL_ASSOCIATION_RATIONALE,
                evidence,
                request.now,
                request.release_mbid,
            )
        if override is None:
            self._session.add(
                SourceAssociationOverrideRecord(
                    source_id=source.id,
                    recording_mbid=request.recording_mbid,
                    actor=request.actor,
                    rationale=_MANUAL_ASSOCIATION_RATIONALE,
                    created_at=request.now,
                )
            )
        else:
            override.recording_mbid = request.recording_mbid
            override.actor = request.actor
            override.rationale = _MANUAL_ASSOCIATION_RATIONALE
            override.created_at = request.now
            override.cleared_at = None
        self._session.flush()
        return result

    def _verify_recording(self, recording_mbid: str, now: datetime) -> None:
        if self._musicbrainz is None:
            raise RecordingAssociationUnavailable(recording_mbid)
        result = self._musicbrainz.lookup(
            MusicBrainzLookupRequest('', FixtureCase.SUCCESS, recording_mbid=recording_mbid), now
        )
        match result:
            case MusicBrainzMatch(candidate=candidate) if recording_mbid in candidate.recording_mbids:
                return
            case Ambiguous(candidates=candidates) if candidates and all(
                recording_mbid in candidate.recording_mbids for candidate in candidates
            ):
                return
            case _:
                raise RecordingAssociationUnavailable(recording_mbid)

    def _associate(
        self,
        source_id: str,
        recording_mbid: str,
        actor: str,
        rationale: str | None,
        evidence_json: str,
        now: datetime,
        release_mbid: str | None,
    ) -> AssociationResult:
        source = self._source(source_id)
        previous_record_id = source.library_record_id
        if previous_record_id is None:
            raise LookupError(source_id)
        target = self._session.scalar(
            select(LibraryRecord)
            .where(LibraryRecord.musicbrainz_recording_id == recording_mbid)
            .where(LibraryRecord.musicbrainz_release_id == release_mbid)
        )
        if target is None:
            try:
                with self._session.begin_nested():
                    target = new_library_record(self._session, now)
                    target.musicbrainz_recording_id = recording_mbid
                    target.musicbrainz_release_id = release_mbid
                    target.match_state = 'matched'
                    self._session.flush()
            except IntegrityError:
                target = self._session.scalar(
                    select(LibraryRecord)
                    .where(LibraryRecord.musicbrainz_recording_id == recording_mbid)
                    .where(LibraryRecord.musicbrainz_release_id == release_mbid)
                )
                if target is None:
                    raise
        locked_records = tuple(
            self._session.scalars(
                select(LibraryRecord)
                .where(LibraryRecord.id.in_(tuple(sorted((previous_record_id, target.id)))))
                .order_by(LibraryRecord.id)
                .options(raiseload('*'), selectinload(LibraryRecord.metadata_revisions))
                .execution_options(populate_existing=True)
                .with_for_update()
            ).all()
        )
        previous_record = next(record for record in locked_records if record.id == previous_record_id)
        locked_target = next(record for record in locked_records if record.id == target.id)
        previous_final = next(
            (
                revision
                for revision in reversed(previous_record.metadata_revisions)
                if revision.source_id == source.id and revision.layer == 'final'
            ),
            None,
        )
        provider_final = next(
            (
                revision
                for revision in reversed(locked_target.metadata_revisions)
                if revision.layer == 'final' and revision.actor == 'provider'
            ),
            None,
        )
        source.library_record_id = locked_target.id
        source.disappeared_at = None
        locked_target.source_state = 'present'
        locked_target.updated_at = now
        inherited_final = provider_final or previous_final
        if inherited_final is not None and previous_record_id != locked_target.id:
            _ = append_metadata_revision(
                self._session,
                locked_target.id,
                source.id,
                'final',
                _TAGS.validate_json(inherited_final.tags_json),
                'reassociation',
                now,
            )
        self._session.add(
            SourceRecordingAssignmentRecord(
                source_id=source.id,
                library_record_id=locked_target.id,
                state='manual_override' if rationale is not None else 'automatic_verified',
                actor=actor,
                rationale=rationale,
                evidence_json=json.dumps(
                    {
                        'after_record_id': locked_target.id,
                        'before_record_id': previous_record_id,
                        'evidence': evidence_json,
                    },
                    sort_keys=True,
                ),
                created_at=now,
            )
        )
        publication_refresh_queued = False
        for record in locked_records:
            self._session.add(
                LibraryEventRecord(
                    library_record_id=record.id,
                    source_id=source.id,
                    kind='source_recording_reassigned',
                    state='matched' if record.id == locked_target.id else 'needs_review',
                    reason=rationale or 'confidence-qualified provider recording evidence',
                    details_json=json.dumps(
                        {
                            'actor': actor,
                            'after_record_id': locked_target.id,
                            'before_record_id': previous_record_id,
                            'recording_mbid': recording_mbid,
                            'release_mbid': release_mbid,
                        },
                        sort_keys=True,
                    ),
                    created_at=now,
                )
            )
        self._session.flush()
        for record in locked_records:
            _ = reevaluate_effective_source_decision(self._session, record.id, now)
            if (
                record.id == locked_target.id
                or self._session.scalar(
                    select(LibraryRecord.id).where(
                        LibraryRecord.id == record.id,
                        LibraryRecord.sources.any()
                        | LibraryRecord.publications.any(LibraryPublicationRecord.state == 'current')
                        | LibraryRecord.publication_attempts.any(
                            PublicationAttemptRecord.state.in_(['reserved', 'staged', 'prepared', 'exposed'])
                        ),
                    )
                )
                is not None
            ):
                publication_refresh_queued = (
                    JobRepository(self._session).enqueue_selection_refresh(record.id, now) is not None
                    or publication_refresh_queued
                )
        return AssociationResult(locked_target.id, previous_record_id, publication_refresh_queued)

    def _source(self, source_id: str) -> SourceRecord:
        source = self._session.scalar(
            select(SourceRecord)
            .where(SourceRecord.id == source_id)
            .options(raiseload('*'), selectinload(SourceRecord.association_override))
            .execution_options(populate_existing=True)
            .with_for_update()
        )
        if source is None:
            raise LookupError(source_id)
        return source

    def _record_review(self, source_id: str, rationale: str, now: datetime) -> None:
        source = self._source(source_id)
        if source.library_record_id is None:
            raise LookupError(source_id)
        record = self._session.get(LibraryRecord, source.library_record_id)
        if record is None:
            raise LookupError(source.library_record_id)
        record.processing_state = 'needs_review'
        record.match_state = 'needs_review'
        record.updated_at = now
        self._session.add(
            ReviewDecisionRecord(source_id=source.id, state='association_review_required', rationale=rationale)
        )
        self._session.add(
            LibraryEventRecord(
                library_record_id=source.library_record_id,
                source_id=source.id,
                kind='recording_association_review_required',
                state='needs_review',
                reason=rationale,
                details_json='{}',
                created_at=now,
            )
        )
        self._session.flush()
