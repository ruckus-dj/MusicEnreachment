from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Final, final, override

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.dto import CandidateEvidencePayload
from music_ingest.library.service import (
    append_metadata_revision,
    new_library_record,
    reevaluate_effective_source_decision,
)
from music_ingest.matching.providers import FixtureCase, MusicBrainzLookupRequest, MusicBrainzMatch, MusicBrainzProvider
from music_ingest.models import (
    LibraryEventRecord,
    LibraryRecord,
    ReviewDecisionRecord,
    SourceAssociationOverrideRecord,
    SourceRecord,
    SourceRecordingAssignmentRecord,
)
from music_ingest.models.jobs import JobRepository

_TAGS: Final = TypeAdapter(dict[str, str])


@dataclass(frozen=True, slots=True)
class AutomaticAssociationRequest:
    source_id: str
    recording_mbid: str
    score: float
    confidence_threshold: float
    evidence_json: str
    now: datetime


@dataclass(frozen=True, slots=True)
class ManualAssociationRequest:
    source_id: str
    recording_mbid: str
    now: datetime


@dataclass(frozen=True, slots=True)
class AssociationResult:
    library_record_id: str
    moved_from_record_id: str


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
        source = self._source(request.source_id)
        if (
            source.association_override is not None
            and source.association_override.cleared_at is None
            or request.score < request.confidence_threshold
            or not self._has_confirmed_recording(request.source_id, request.recording_mbid)
        ):
            self._record_review(
                request.source_id, 'automatic recording lacks confirmed MusicBrainz recording evidence', request.now
            )
            return None
        if self._has_conflicting_content_recording(source, request.recording_mbid):
            self._record_review(
                request.source_id,
                'exact-content source has conflicting automatic recording identity',
                request.now,
            )
            return None
        return self._associate(
            request.source_id, request.recording_mbid, 'automatic', None, request.evidence_json, request.now
        )

    def associate_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        self._verify_recording(request.recording_mbid, request.now)
        source = self._source(request.source_id)
        evidence = json.dumps(
            {'recording_mbid': request.recording_mbid},
            sort_keys=True,
        )
        result = self._associate(request.source_id, request.recording_mbid, 'manual', None, evidence, request.now)
        override = source.association_override
        if override is None:
            self._session.add(
                SourceAssociationOverrideRecord(
                    source_id=source.id,
                    recording_mbid=request.recording_mbid,
                    actor='manual',
                    rationale=None,
                    created_at=request.now,
                )
            )
        else:
            override.recording_mbid = request.recording_mbid
            override.actor = 'manual'
            override.rationale = None
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
    ) -> AssociationResult:
        source = self._source(source_id)
        previous_record_id = source.library_record_id
        if previous_record_id is None:
            raise LookupError(source_id)
        target = self._session.scalar(
            select(LibraryRecord).where(LibraryRecord.musicbrainz_recording_id == recording_mbid).with_for_update()
        )
        if target is None:
            try:
                with self._session.begin_nested():
                    target = new_library_record(self._session, now)
                    target.musicbrainz_recording_id = recording_mbid
                    target.match_state = 'matched'
                    self._session.flush()
            except IntegrityError:
                target = self._session.scalar(
                    select(LibraryRecord)
                    .where(LibraryRecord.musicbrainz_recording_id == recording_mbid)
                    .with_for_update()
                )
                if target is None:
                    raise
        locked_records = tuple(
            self._session.scalars(
                select(LibraryRecord)
                .where(LibraryRecord.id.in_(tuple(sorted((previous_record_id, target.id)))))
                .order_by(LibraryRecord.id)
                .with_for_update()
            ).all()
        )
        previous_record = next(record for record in locked_records if record.id == previous_record_id)
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
                for revision in reversed(target.metadata_revisions)
                if revision.layer == 'final' and revision.actor == 'provider'
            ),
            None,
        )
        source.library_record_id = target.id
        source.disappeared_at = None
        target.source_state = 'present'
        target.updated_at = now
        inherited_final = provider_final or previous_final
        if inherited_final is not None and previous_record_id != target.id:
            _ = append_metadata_revision(
                self._session,
                target.id,
                source.id,
                'final',
                _TAGS.validate_json(inherited_final.tags_json),
                'reassociation',
                now,
            )
        self._session.add(
            SourceRecordingAssignmentRecord(
                source_id=source.id,
                library_record_id=target.id,
                state='manual_override' if rationale is not None else 'automatic_verified',
                actor=actor,
                rationale=rationale,
                evidence_json=json.dumps(
                    {'after_record_id': target.id, 'before_record_id': previous_record_id, 'evidence': evidence_json},
                    sort_keys=True,
                ),
                created_at=now,
            )
        )
        for record in locked_records:
            self._session.add(
                LibraryEventRecord(
                    library_record_id=record.id,
                    source_id=source.id,
                    kind='source_recording_reassigned',
                    state='matched' if record.id == target.id else 'needs_review',
                    reason=rationale or 'confidence-qualified provider recording evidence',
                    details_json=json.dumps(
                        {
                            'actor': actor,
                            'after_record_id': target.id,
                            'before_record_id': previous_record_id,
                            'recording_mbid': recording_mbid,
                        },
                        sort_keys=True,
                    ),
                    created_at=now,
                )
            )
        self._session.flush()
        for record in locked_records:
            _ = reevaluate_effective_source_decision(self._session, record.id, now)
            _ = JobRepository(self._session).enqueue_selection_refresh(record.id, now)
        return AssociationResult(target.id, previous_record_id)

    def _source(self, source_id: str) -> SourceRecord:
        source = self._session.scalar(select(SourceRecord).where(SourceRecord.id == source_id).with_for_update())
        if source is None:
            raise LookupError(source_id)
        return source

    def _has_confirmed_recording(self, source_id: str, recording_mbid: str) -> bool:
        source = self._source(source_id)
        has_musicbrainz_recording_evidence = any(
            attempt.provider_name == 'musicbrainz' and attempt.outcome in {'musicbrainzmatch', 'ambiguous'}
            for attempt in source.provider_attempts
        )
        if not has_musicbrainz_recording_evidence:
            return False
        return any(
            evidence.provider == 'musicbrainz'
            and (evidence.tags.get('MUSICBRAINZ_RECORDINGID') or evidence.tags.get('MUSICBRAINZ_TRACKID'))
            == recording_mbid
            for candidate in source.candidates
            for evidence in (CandidateEvidencePayload.model_validate_json(candidate.evidence),)
        )

    def _has_conflicting_content_recording(self, source: SourceRecord, recording_mbid: str) -> bool:
        return (
            self._session.scalar(
                select(SourceRecord.id)
                .join(LibraryRecord, SourceRecord.library_record_id == LibraryRecord.id)
                .where(SourceRecord.sha256 == source.sha256)
                .where(SourceRecord.id != source.id)
                .where(LibraryRecord.musicbrainz_recording_id.is_not(None))
                .where(LibraryRecord.musicbrainz_recording_id != recording_mbid)
            )
            is not None
        )

    def _record_review(self, source_id: str, rationale: str, now: datetime) -> None:
        source = self._source(source_id)
        if source.library_record_id is None:
            raise LookupError(source_id)
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
