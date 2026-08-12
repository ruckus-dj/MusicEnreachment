from __future__ import annotations

import hashlib
import json
from collections.abc import Iterable
from dataclasses import dataclass
from enum import StrEnum
from typing import Final, cast

POLICY_VERSION: Final = 'quality-policy-v1'
_LOSSLESS_CODECS: Final = frozenset({'FLAC', 'ALAC'})
_LOSSY_CODECS: Final = frozenset({'OPUS', 'AAC', 'MP3', 'VORBIS'})
_CODEC_RANK: Final = {'FLAC': 5, 'ALAC': 4, 'OPUS': 3, 'AAC': 2, 'MP3': 1, 'VORBIS': 0}
_EXCLUDED_INTAKE_STATES: Final = frozenset({'disappeared', 'quarantined', 'unsupported', 'failed', 'invalid_audio'})


class Codec(StrEnum):
    FLAC = 'FLAC'
    ALAC = 'ALAC'
    OPUS = 'OPUS'
    AAC = 'AAC'
    MP3 = 'MP3'
    VORBIS = 'VORBIS'


class DecisionReason(StrEnum):
    INITIAL_SELECTION = 'initial_selection'
    MANUAL_BASELINE = 'manual_baseline'
    MANUAL_BASELINE_RETAINED = 'manual_baseline_retained'
    STRICTLY_BETTER_REPLACEMENT = 'strictly_better_replacement'
    CURRENT_SELECTION_RETAINED = 'current_selection_retained'
    INELIGIBLE_SOURCE_REPLACED = 'ineligible_source_replaced'
    POLICY_VERSION_REVIEW = 'policy_version_review'
    NO_ELIGIBLE_SOURCE = 'no_eligible_source'


@dataclass(frozen=True, slots=True)
class QualityCandidate:
    source_id: str
    codec: Codec | str
    bit_depth: object | None
    sample_rate: object | None
    channels: object | None
    bitrate: object | None
    confirmed: bool
    intake_state: str
    disappeared: bool


QualityTuple = tuple[int, int, int, int, int, int, int]


@dataclass(frozen=True, slots=True)
class QualityDecision:
    source_id: str | None
    baseline_source_id: str | None
    policy_version: str
    quality_tuple: QualityTuple | None
    reason: DecisionReason
    candidate_set_key: str


@dataclass(frozen=True, slots=True)
class ExistingDecision:
    source_id: str | None
    baseline_source_id: str | None
    policy_version: str
    quality_tuple: QualityTuple | None
    reason: DecisionReason
    candidate_set_key: str

    @classmethod
    def from_decision(cls, decision: QualityDecision, *, policy_version: str = POLICY_VERSION) -> ExistingDecision:
        return cls(
            source_id=decision.source_id,
            baseline_source_id=decision.baseline_source_id,
            policy_version=policy_version,
            quality_tuple=decision.quality_tuple,
            reason=decision.reason,
            candidate_set_key=decision.candidate_set_key,
        )


def evaluate(
    candidates: Iterable[QualityCandidate],
    *,
    previous: ExistingDecision | None = None,
    manual_source_id: str | None = None,
    candidate_set_key: str | None = None,
) -> QualityDecision:
    candidate_list = tuple(candidates)
    resolved_set_key = candidate_set_key or _candidate_set_key(candidate_list)
    eligible = tuple(candidate for candidate in candidate_list if quality_tuple(candidate) is not None)
    by_id = {candidate.source_id: candidate for candidate in eligible}
    if manual_source_id is not None:
        manual = by_id.get(manual_source_id)
        if manual is None:
            raise ValueError('manual source must be eligible')
        return _decision(manual, manual.source_id, DecisionReason.MANUAL_BASELINE, resolved_set_key)
    if previous is not None and previous.policy_version != POLICY_VERSION:
        return QualityDecision(
            source_id=previous.source_id,
            baseline_source_id=previous.baseline_source_id,
            policy_version=POLICY_VERSION,
            quality_tuple=previous.quality_tuple,
            reason=DecisionReason.POLICY_VERSION_REVIEW,
            candidate_set_key=resolved_set_key,
        )
    winner = _best(eligible)
    if previous is None or previous.source_id is None or previous.quality_tuple is None:
        return _empty_or_initial(winner, resolved_set_key)
    current = by_id.get(previous.source_id)
    if previous.baseline_source_id is not None:
        if current is None and winner is not None:
            return _decision(
                winner, previous.baseline_source_id, DecisionReason.INELIGIBLE_SOURCE_REPLACED, resolved_set_key
            )
        if resolved_set_key == previous.candidate_set_key:
            return _previous(previous, previous.reason, resolved_set_key)
        if winner is None:
            return _previous(previous, DecisionReason.MANUAL_BASELINE_RETAINED, resolved_set_key)
        if _strictly_better(winner, previous.quality_tuple):
            return _decision(
                winner,
                previous.baseline_source_id,
                DecisionReason.STRICTLY_BETTER_REPLACEMENT,
                resolved_set_key,
            )
        return _previous(previous, DecisionReason.MANUAL_BASELINE_RETAINED, resolved_set_key)
    if winner is None:
        return _previous(previous, DecisionReason.CURRENT_SELECTION_RETAINED, resolved_set_key)
    if current is None:
        return _decision(winner, None, DecisionReason.INELIGIBLE_SOURCE_REPLACED, resolved_set_key)
    if not _strictly_better(winner, previous.quality_tuple):
        return _previous(previous, DecisionReason.CURRENT_SELECTION_RETAINED, resolved_set_key)
    if _strictly_better(winner, previous.quality_tuple):
        return _decision(winner, None, DecisionReason.STRICTLY_BETTER_REPLACEMENT, resolved_set_key)
    return _previous(previous, DecisionReason.CURRENT_SELECTION_RETAINED, resolved_set_key)


def quality_tuple(candidate: QualityCandidate) -> QualityTuple | None:
    codec = _codec_name(candidate.codec)
    if not candidate.confirmed or candidate.disappeared or candidate.intake_state.casefold() in _EXCLUDED_INTAKE_STATES:
        return None
    if codec in _LOSSLESS_CODECS:
        if not _positive(candidate.bit_depth, candidate.sample_rate, candidate.channels) or candidate.bitrate not in (
            None,
            0,
        ):
            return None
        return (
            1,
            2,
            _CODEC_RANK[codec],
            *cast(tuple[int, int, int], (candidate.bit_depth, candidate.sample_rate, candidate.channels)),
            0,
        )
    if codec in _LOSSY_CODECS:
        if not _positive(candidate.sample_rate, candidate.channels, candidate.bitrate) or candidate.bit_depth not in (
            None,
            0,
        ):
            return None
        return (
            1,
            1,
            _CODEC_RANK[codec],
            0,
            *cast(tuple[int, int, int], (candidate.sample_rate, candidate.channels, candidate.bitrate)),
        )
    return None


def _empty_or_initial(winner: QualityCandidate | None, candidate_set_key: str) -> QualityDecision:
    if winner is None:
        return QualityDecision(None, None, POLICY_VERSION, None, DecisionReason.NO_ELIGIBLE_SOURCE, candidate_set_key)
    return _decision(winner, None, DecisionReason.INITIAL_SELECTION, candidate_set_key)


def _best(candidates: tuple[QualityCandidate, ...]) -> QualityCandidate | None:
    return (
        min(
            candidates,
            key=lambda candidate: tuple(-value for value in quality_tuple(candidate) or ()) + (candidate.source_id,),
        )
        if candidates
        else None
    )


def _decision(
    source: QualityCandidate, baseline_source_id: str | None, reason: DecisionReason, candidate_set_key: str
) -> QualityDecision:
    return QualityDecision(
        source.source_id, baseline_source_id, POLICY_VERSION, quality_tuple(source), reason, candidate_set_key
    )


def _previous(previous: ExistingDecision, reason: DecisionReason, candidate_set_key: str) -> QualityDecision:
    return QualityDecision(
        previous.source_id,
        previous.baseline_source_id,
        POLICY_VERSION,
        previous.quality_tuple,
        reason,
        candidate_set_key,
    )


def _strictly_better(candidate: QualityCandidate, previous: QualityTuple) -> bool:
    candidate_tuple = quality_tuple(candidate)
    return candidate_tuple is not None and candidate_tuple > previous


def _codec_name(codec: Codec | str) -> str:
    return codec.value if isinstance(codec, Codec) else codec.upper()


def _positive(*values: object | None) -> bool:
    return all(type(value) is int and value > 0 for value in values)


def _candidate_set_key(candidates: tuple[QualityCandidate, ...]) -> str:
    payload = [
        {
            'bit_depth': candidate.bit_depth,
            'bitrate': candidate.bitrate,
            'channels': candidate.channels,
            'codec': _codec_name(candidate.codec),
            'confirmed': candidate.confirmed,
            'disappeared': candidate.disappeared,
            'intake_state': candidate.intake_state,
            'sample_rate': candidate.sample_rate,
            'source_id': candidate.source_id,
        }
        for candidate in sorted(candidates, key=lambda item: item.source_id)
    ]
    return hashlib.sha256(json.dumps(payload, separators=(',', ':'), sort_keys=True).encode()).hexdigest()
