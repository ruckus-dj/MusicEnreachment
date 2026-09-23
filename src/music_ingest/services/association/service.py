from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Final, final, override

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.models import (
    LibraryEventRecord,
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

    def associate_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        self._verify_recording(request.recording_mbid, request.now)
        return self._associate_manual(request)

    def associate_verified_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        """Reassign using already persisted provider evidence selected by a reviewer."""
        return self._associate_manual(request)

    def _associate_manual(self, request: ManualAssociationRequest) -> AssociationResult:
        source = self._source(request.source_id)
        evidence = json.dumps(
            {'recording_mbid': request.recording_mbid},
            sort_keys=True,
        )
        result = self._associate(
            request.source_id,
            request.recording_mbid,
            'manual',
            _MANUAL_ASSOCIATION_RATIONALE,
            evidence,
            request.now,
            request.release_mbid,
        )
        override = source.association_override
        if override is None:
            self._session.add(
                SourceAssociationOverrideRecord(
                    source_id=source.id,
                    recording_mbid=request.recording_mbid,
                    actor='manual',
                    rationale=_MANUAL_ASSOCIATION_RATIONALE,
                    created_at=request.now,
                )
            )
        else:
            override.recording_mbid = request.recording_mbid
            override.actor = 'manual'
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
            .with_for_update()
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
                record.id == target.id
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
                _ = JobRepository(self._session).enqueue_selection_refresh(record.id, now)
        return AssociationResult(target.id, previous_record_id)

    def _source(self, source_id: str) -> SourceRecord:
        source = self._session.scalar(select(SourceRecord).where(SourceRecord.id == source_id).with_for_update())
        if source is None:
            raise LookupError(source_id)
        return source

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
