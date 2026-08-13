from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from uuid import uuid4

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.dto import (
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
from music_ingest.models.repositories import IntakeRepository

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
    source_hash = _source_sha256(request.source_path)
    source_id = _source_id(request.source_root_id, source_stat.st_dev, source_stat.st_ino, source_hash)
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
                    source_hash,
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
    source_hash: str,
) -> SourceRecord:
    now = datetime.now(UTC)
    library_record = LibraryRecord(id=f'record-{uuid4().hex}', created_at=now, updated_at=now)
    source = SourceRecord(
        id=source_id,
        source_path=str(request.source_path),
        device=device,
        inode=inode,
        size_bytes=size_bytes,
        sha256=source_hash,
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
    existing_source = repository.find_other_source_by_sha256(source_hash, source.id)
    if existing_source is not None and existing_source.library_record is not None:
        source.library_record = existing_source.library_record
        _ = repository.add_source(source)
    return source


def _source_sha256(source_path: Path) -> str:
    digest = sha256()
    with source_path.open('rb') as source_file:
        for chunk in iter(lambda: source_file.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _source_id(source_root_id: str, device: int, inode: int, source_hash: str) -> SourceId:
    return SourceId(sha256(f'{source_root_id}:{device}:{inode}:{source_hash}'.encode()).hexdigest())
