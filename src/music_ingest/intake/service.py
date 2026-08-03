from __future__ import annotations

import json
from enum import StrEnum
from hashlib import sha256
from pathlib import Path
from typing import Annotated, ClassVar, NewType

from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.persistence.models import (
    ArtworkRecord,
    CandidateRecord,
    ProviderAttemptRecord,
    PublicationRecord,
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
    provenance_path: Path


class ProvenanceTag(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    format_name: str
    tag_name: str
    value: str


class ProvenanceProviderAttempt(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    provider_name: str
    outcome: str
    snapshot_sha256: str


class ProvenanceArtifact(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    source_id: str
    source_path: str
    device: int
    inode: int
    size_bytes: int
    sha256: str
    duration_seconds: int | None
    origin: Origin
    intake_state: IntakeState
    observed_tags: tuple[ProvenanceTag, ...]
    artwork_hashes: tuple[str, ...]
    provider_attempts: tuple[ProvenanceProviderAttempt, ...]
    candidates: tuple[CandidateEvidence, ...]
    review_decisions: tuple[ReviewDecision, ...]


def intake_source(session: Session, request: IntakeRequest, provenance_directory: Path) -> IntakeResult:
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
    artifact_path = _write_provenance(provenance_directory, source)
    return IntakeResult(source_id=source_id, provenance_path=artifact_path)


def _persist_source(
    repository: IntakeRepository,
    request: IntakeRequest,
    source_id: SourceId,
    device: int,
    inode: int,
    size_bytes: int,
    source_hash: str,
) -> SourceRecord:
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
    source.publication = PublicationRecord(publication_state='pending', published_path=None)
    return repository.add_source(source)


def _source_sha256(source_path: Path) -> str:
    digest = sha256()
    with source_path.open('rb') as source_file:
        for chunk in iter(lambda: source_file.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _source_id(device: int, inode: int, source_hash: str) -> SourceId:
    return SourceId(sha256(f'{device}:{inode}:{source_hash}'.encode()).hexdigest())


def _write_provenance(provenance_directory: Path, source: SourceRecord) -> Path:
    artifact = ProvenanceArtifact(
        source_id=source.id,
        source_path=source.source_path,
        device=source.device,
        inode=source.inode,
        size_bytes=source.size_bytes,
        sha256=source.sha256,
        duration_seconds=source.duration_seconds,
        origin=Origin(source.origin),
        intake_state=IntakeState(source.intake_state),
        observed_tags=tuple(
            ProvenanceTag(format_name=item.format_name, tag_name=item.tag_name, value=item.value)
            for item in source.tag_observations
        ),
        artwork_hashes=tuple(item.sha256 for item in source.artwork_observations),
        provider_attempts=tuple(
            ProvenanceProviderAttempt(
                provider_name=item.provider_name,
                outcome=item.outcome,
                snapshot_sha256=item.snapshot_sha256,
            )
            for item in source.provider_attempts
        ),
        candidates=tuple(
            CandidateEvidence(candidate_key=item.candidate_key, evidence=item.evidence) for item in source.candidates
        ),
        review_decisions=tuple(
            ReviewDecision(state=item.state, rationale=item.rationale) for item in source.review_decisions
        ),
    )
    provenance_directory.mkdir(parents=True, exist_ok=True)
    artifact_path = provenance_directory / f'{source.id}.json'
    if artifact_path.exists():
        return artifact_path
    with artifact_path.open('x', encoding='utf-8') as artifact_file:
        artifact_json = json.dumps(artifact.model_dump(mode='json'), sort_keys=True, separators=(',', ':'))
        _ = artifact_file.write(f'{artifact_json}\n')
    return artifact_path
