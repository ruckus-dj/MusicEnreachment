from __future__ import annotations

from datetime import UTC, datetime
from enum import StrEnum
from hashlib import sha256
from pathlib import Path
from typing import Annotated, ClassVar, NewType
from uuid import uuid4

from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.persistence.library import LibraryRecord
from music_ingest.persistence.models import (
    ArtworkRecord,
    CandidateRecord,
    ProviderAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceTagRecord,
)
from music_ingest.persistence.repository import IntakeRepository

SourceId = NewType('SourceId', str)
type NonEmptyText = Annotated[str, Field(min_length=1, strict=True)]
type Sha256 = Annotated[str, Field(pattern=r'^[0-9A-Fa-f]{64}$', strict=True)]
type NonNegativeDuration = Annotated[int, Field(ge=0, strict=True)]


class Origin(StrEnum):
    MANUAL = 'manual'
    LIDARR = 'lidarr'


class IntakeState(StrEnum):
    NEEDS_REVIEW = 'needs_review'


class IntakeEvidence(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, strict=True)


class SourceTagObservation(IntakeEvidence):
    format_name: NonEmptyText
    tag_name: NonEmptyText
    value: NonEmptyText


class ArtworkObservation(IntakeEvidence):
    sha256: Sha256


class ProviderAttempt(IntakeEvidence):
    provider_name: NonEmptyText
    outcome: NonEmptyText
    snapshot_sha256: Sha256
    snapshot: NonEmptyText


class CandidateEvidence(IntakeEvidence):
    candidate_key: NonEmptyText
    evidence: NonEmptyText


class ReviewDecision(IntakeEvidence):
    state: NonEmptyText
    rationale: NonEmptyText


class IntakeRequest(IntakeEvidence):
    source_path: Path
    origin: Origin
    duration_seconds: NonNegativeDuration | None
    tag_observations: tuple[SourceTagObservation, ...]
    artwork_observations: tuple[ArtworkObservation, ...]
    provider_attempts: tuple[ProviderAttempt, ...]
    candidates: tuple[CandidateEvidence, ...]
    review_decisions: tuple[ReviewDecision, ...]


class IntakeResult(IntakeEvidence):
    source_id: SourceId


def intake_source(session: Session, request: IntakeRequest) -> IntakeResult:
    source_stat = request.source_path.stat()
    source_hash = _source_sha256(request.source_path)
    source_id = _source_id(source_stat.st_dev, source_stat.st_ino, source_hash)
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
    return repository.add_source(source)


def _source_sha256(source_path: Path) -> str:
    digest = sha256()
    with source_path.open('rb') as source_file:
        for chunk in iter(lambda: source_file.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _source_id(device: int, inode: int, source_hash: str) -> SourceId:
    return SourceId(sha256(f'{device}:{inode}:{source_hash}'.encode()).hexdigest())
