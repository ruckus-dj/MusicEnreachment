from __future__ import annotations

from enum import StrEnum
from pathlib import Path
from typing import Annotated, ClassVar, NewType

from pydantic import BaseModel, ConfigDict, Field

SourceId = NewType('SourceId', str)
type NonEmptyText = Annotated[str, Field(min_length=1, strict=True)]
type Sha256 = Annotated[str, Field(pattern=r'^[0-9A-Fa-f]{64}$', strict=True)]
type NonNegativeDuration = Annotated[int, Field(ge=0, strict=True)]


class Origin(StrEnum):
    MANUAL = 'manual'


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
    source_root_id: str = 'legacy'
    origin: Origin
    duration_seconds: NonNegativeDuration | None
    tag_observations: tuple[SourceTagObservation, ...]
    artwork_observations: tuple[ArtworkObservation, ...]
    provider_attempts: tuple[ProviderAttempt, ...]
    candidates: tuple[CandidateEvidence, ...]
    review_decisions: tuple[ReviewDecision, ...]


class IntakeResult(IntakeEvidence):
    source_id: SourceId
    created: bool
