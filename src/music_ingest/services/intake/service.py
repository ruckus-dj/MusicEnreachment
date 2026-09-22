from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from uuid import uuid4

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.contracts import (
    ArtworkObservation,
    CandidateEvidence,
    IntakeEvidence,
    IntakeRequest,
    IntakeResult,
    IntakeState,
    Origin,
    ProviderAttempt,
    ReviewDecision,
    SourceId,
    SourceTagObservation,
)
from music_ingest.models import (
    ArtworkRecord,
    CandidateRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceTagRecord,
)
from music_ingest.repositories.persistence import IntakeRepository

__all__ = [
    'ArtworkObservation',
    'CandidateEvidence',
    'IntakeEvidence',
    'IntakeRequest',
    'IntakeResult',
    'IntakeState',
    'Origin',
    'ProviderAttempt',
    'ReviewDecision',
    'SourceId',
    'SourceTagObservation',
    'intake_source',
]


def intake_source(session: Session, request: IntakeRequest) -> IntakeResult:
    source_stat = request.source_path.stat()
    source_fingerprint = _source_fingerprint(source_stat.st_size, source_stat.st_mtime_ns, source_stat.st_ino)
    source_id = _source_id(request.source_root_id, source_stat.st_dev, source_stat.st_ino, source_fingerprint)
    repository = IntakeRepository(session)
    source = repository.find_source(source_id)
    if source is None:
        try:
            with session.begin_nested():
                source = _persist_source(
                    repository,
                    request,
                    source_id,
                    source_stat.st_dev,
                    source_stat.st_ino,
                    source_stat.st_size,
                    source_stat.st_mtime_ns,
                    source_fingerprint,
                )
        except IntegrityError:
            session.expire_all()
            source = repository.find_source(source_id)
            if source is None:
                raise
    return IntakeResult(source_id=source_id)


def _persist_source(
    repository: IntakeRepository,
    request: IntakeRequest,
    source_id: SourceId,
    device: int,
    inode: int,
    size_bytes: int,
    mtime_ns: int,
    source_fingerprint: str,
) -> SourceRecord:
    now = datetime.now(UTC)
    library_record = LibraryRecord(id=f'record-{uuid4().hex}', created_at=now, updated_at=now)
    source = SourceRecord(
        id=source_id,
        source_path=str(request.source_path),
        device=device,
        inode=inode,
        size_bytes=size_bytes,
        sha256=source_fingerprint,
        mtime_ns=mtime_ns,
        duration_seconds=request.duration_seconds,
        origin=request.origin.value,
        intake_state=IntakeState.NEEDS_REVIEW.value,
        source_root_id=request.source_root_id,
        library_record=library_record,
    )
    source.tag_observations = [
        SourceTagRecord(format_name=item.format_name, tag_name=item.tag_name, value=item.value)
        for item in request.tag_observations
    ]
    source.artwork_observations = [ArtworkRecord(sha256=item.sha256) for item in request.artwork_observations]
    source.provider_attempts = [
        ProviderAttemptRecord(
            provider_name=item.provider_name,
            outcome=item.outcome,
            snapshot_sha256=item.snapshot_sha256,
            snapshot=item.snapshot,
        )
        for item in request.provider_attempts
    ]
    source.candidates = [
        CandidateRecord(candidate_key=item.candidate_key, evidence=item.evidence) for item in request.candidates
    ]
    source.review_decisions = [
        ReviewDecisionRecord(state=item.state, rationale=item.rationale) for item in request.review_decisions
    ]
    source = repository.add_source(source)
    return source


def _source_fingerprint(size_bytes: int, mtime_ns: int, inode: int) -> str:
    return sha256(f'{size_bytes}:{mtime_ns}:{inode}'.encode()).hexdigest()


def _source_id(source_root_id: str, device: int, inode: int, source_hash: str) -> SourceId:
    return SourceId(sha256(f'{source_root_id}:{device}:{inode}:{source_hash}'.encode()).hexdigest())
