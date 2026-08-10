from __future__ import annotations

import unicodedata
from dataclasses import dataclass
from enum import StrEnum
from typing import Final

from music_ingest.matching.providers import (
    AcoustIdMatch,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    LiveProvenance,
    Malformed,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    ReleaseCandidate,
    Timeout,
    Unavailable,
)


class MatchDecision(StrEnum):
    AUTO_SELECTED = 'auto_selected'
    NEEDS_REVIEW = 'needs_review'
    LOCAL_ONLY_REVIEW = 'local_only_review'


class ReviewReason(StrEnum):
    LOCAL_ONLY_REQUESTED = 'local_only_requested'
    MANUAL_MBID_UNVERIFIED = 'manual_mbid_unverified'
    MUSICBRAINZ_STALE = 'musicbrainz_stale'
    MUSICBRAINZ_AMBIGUOUS = 'musicbrainz_ambiguous'
    MUSICBRAINZ_UNAVAILABLE = 'musicbrainz_unavailable'
    MUSICBRAINZ_UNKNOWN = 'musicbrainz_unknown'
    CONFLICTING_CONTEXT = 'conflicting_context'
    INSUFFICIENT_RELEASE_SCORE = 'insufficient_release_score'
    UNSAFE_TEXT = 'unsafe_text'


DEFAULT_CONFIDENCE_THRESHOLD: Final = 0.70


@dataclass(frozen=True, slots=True)
class ExplicitMusicBrainzIds:
    release_mbid: str | None = None
    recording_mbid: str | None = None
    track_mbid: str | None = None


@dataclass(frozen=True, slots=True)
class LidarrContext:
    artist_name: str
    release_title: str
    duration_seconds: int | None


@dataclass(frozen=True, slots=True)
class MatchingRequest:
    artist_name: str
    release_title: str
    duration_seconds: int | None
    explicit_ids: ExplicitMusicBrainzIds = ExplicitMusicBrainzIds()
    lidarr: LidarrContext | None = None
    local_only: bool = False


@dataclass(frozen=True, slots=True)
class CandidateScore:
    candidate_mbid: str | None
    score: float


@dataclass(frozen=True, slots=True)
class MatchResult:
    decision: MatchDecision
    selected_release_mbid: str | None
    recording_score: CandidateScore
    release_score: CandidateScore
    review_reason: ReviewReason | None
    candidate_scores: tuple[CandidateScore, ...] = ()


def resolve_match(
    request: MatchingRequest,
    musicbrainz: MusicBrainzResult,
    acoustid: AcoustIdResult | None,
    confidence_threshold: float = DEFAULT_CONFIDENCE_THRESHOLD,
) -> MatchResult:
    recording_score = _recording_score(request.explicit_ids, acoustid)
    candidate_scores = _candidate_scores(request, musicbrainz)
    release_score = candidate_scores[0] if candidate_scores else CandidateScore(None, 0.0)
    if request.local_only:
        return MatchResult(
            MatchDecision.LOCAL_ONLY_REVIEW,
            None,
            recording_score,
            release_score,
            ReviewReason.LOCAL_ONLY_REQUESTED,
            candidate_scores,
        )
    automatic_candidate = _unique_source_match(request, musicbrainz)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            recording_score,
            release_score,
            None,
            candidate_scores,
        )
    reason = _review_reason(request, musicbrainz, release_score, confidence_threshold)
    if reason is not None:
        return MatchResult(MatchDecision.NEEDS_REVIEW, None, recording_score, release_score, reason, candidate_scores)
    match musicbrainz:
        case MusicBrainzMatch(candidate=candidate):
            return MatchResult(
                MatchDecision.AUTO_SELECTED,
                candidate.release_mbid,
                recording_score,
                release_score,
                None,
                candidate_scores,
            )
        case Ambiguous() | Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout() | Unavailable():
            return MatchResult(
                MatchDecision.NEEDS_REVIEW,
                None,
                recording_score,
                release_score,
                ReviewReason.MUSICBRAINZ_UNKNOWN,
                candidate_scores,
            )


def _recording_score(explicit_ids: ExplicitMusicBrainzIds, acoustid: AcoustIdResult | None) -> CandidateScore:
    match acoustid:
        case AcoustIdMatch(evidence=evidence):
            exact_score = 1.0 if explicit_ids.recording_mbid == evidence.recording_mbid else evidence.score
            return CandidateScore(evidence.recording_mbid, exact_score)
        case None | Ambiguous() | Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout() | Unavailable():
            return CandidateScore(explicit_ids.recording_mbid, 0.0)


def _candidate_scores(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> tuple[CandidateScore, ...]:
    match musicbrainz:
        case MusicBrainzMatch(candidate=candidate):
            return (CandidateScore(candidate.release_mbid, _candidate_score(request, candidate)),)
        case Ambiguous(candidates=candidates):
            return tuple(
                CandidateScore(candidate.release_mbid, _candidate_score(request, candidate)) for candidate in candidates
            )
        case Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout() | Unavailable():
            return ()


def _unique_source_match(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> ReleaseCandidate | None:
    """Return the sole candidate matching both source artist and album text."""
    if request.explicit_ids.release_mbid or request.explicit_ids.recording_mbid or request.explicit_ids.track_mbid:
        return None
    if not request.artist_name.strip() or not request.release_title.strip() or _has_unsafe_text(request):
        return None
    match musicbrainz:
        case Ambiguous(provenance=LiveProvenance(state='fresh' | 'cached'), candidates=candidates):
            matches = tuple(
                candidate
                for candidate in candidates
                if _normalized(request.artist_name) == _normalized(candidate.artist_name)
                and _normalized(request.release_title) == _normalized(candidate.release_title)
                and not _candidate_has_unsafe_text(candidate)
            )
            return matches[0] if len(matches) == 1 else None
        case MusicBrainzMatch() | Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout() | Unavailable():
            return None


def _candidate_score(request: MatchingRequest, candidate: ReleaseCandidate) -> float:
    if _candidate_matches_explicit_ids(request.explicit_ids, candidate):
        return 1.0
    source_score = _text_duration_score(
        request.artist_name,
        request.release_title,
        request.duration_seconds,
        candidate,
    )
    if request.lidarr is None:
        return source_score
    lidarr_score = _text_duration_score(
        request.lidarr.artist_name,
        request.lidarr.release_title,
        request.lidarr.duration_seconds,
        candidate,
    )
    return max(source_score, lidarr_score)


def _text_duration_score(
    artist_name: str, release_title: str, duration_seconds: int | None, candidate: ReleaseCandidate
) -> float:
    artist_score = 0.4 if _normalized(artist_name) == _normalized(candidate.artist_name) else 0.0
    title_score = 0.4 if _normalized(release_title) == _normalized(candidate.release_title) else 0.0
    return artist_score + title_score + _duration_score(duration_seconds, candidate.duration_seconds)


def _duration_score(expected: int | None, actual: int | None) -> float:
    if expected is None or actual is None:
        return 0.0
    difference = abs(expected - actual)
    if difference <= 2:
        return 0.2
    if difference <= 5:
        return 0.1
    return 0.0


def _review_reason(
    request: MatchingRequest,
    musicbrainz: MusicBrainzResult,
    release_score: CandidateScore,
    confidence_threshold: float,
) -> ReviewReason | None:
    match musicbrainz:
        case MusicBrainzMatch(provenance=LiveProvenance(state='stale')):
            return ReviewReason.MUSICBRAINZ_STALE
        case MusicBrainzMatch(candidate=candidate):
            if _has_unsafe_text(request) or _candidate_has_unsafe_text(candidate):
                return ReviewReason.UNSAFE_TEXT
            if _manual_mbid_is_unverified(request.explicit_ids, candidate):
                return ReviewReason.MANUAL_MBID_UNVERIFIED
            if request.lidarr is not None and _lidarr_conflicts(request.lidarr, candidate):
                return ReviewReason.CONFLICTING_CONTEXT
            if release_score.score < confidence_threshold:
                return ReviewReason.INSUFFICIENT_RELEASE_SCORE
            match candidate, musicbrainz.provenance:
                case _, LiveProvenance(state='fresh' | 'cached'):
                    return None
                case _, LiveProvenance():
                    return ReviewReason.MUSICBRAINZ_STALE
                case _, _:
                    return ReviewReason.MUSICBRAINZ_UNKNOWN
        case Ambiguous():
            return ReviewReason.MUSICBRAINZ_AMBIGUOUS
        case Unavailable() | RateLimited() | Timeout():
            return (
                ReviewReason.MANUAL_MBID_UNVERIFIED
                if _has_manual_mbid(request.explicit_ids)
                else ReviewReason.MUSICBRAINZ_UNAVAILABLE
            )
        case Disabled() | Malformed() | NoMatch():
            return (
                ReviewReason.MANUAL_MBID_UNVERIFIED
                if _has_manual_mbid(request.explicit_ids)
                else ReviewReason.MUSICBRAINZ_UNKNOWN
            )


def _manual_mbid_is_unverified(explicit_ids: ExplicitMusicBrainzIds, candidate: ReleaseCandidate) -> bool:
    return (
        (explicit_ids.release_mbid is not None and explicit_ids.release_mbid != candidate.release_mbid)
        or (explicit_ids.recording_mbid is not None and explicit_ids.recording_mbid not in candidate.recording_mbids)
        or (explicit_ids.track_mbid is not None and explicit_ids.track_mbid not in candidate.track_mbids)
    )


def _has_manual_mbid(explicit_ids: ExplicitMusicBrainzIds) -> bool:
    return any((explicit_ids.release_mbid, explicit_ids.recording_mbid, explicit_ids.track_mbid))


def _candidate_matches_explicit_ids(explicit_ids: ExplicitMusicBrainzIds, candidate: ReleaseCandidate) -> bool:
    return (
        explicit_ids.release_mbid == candidate.release_mbid
        or explicit_ids.recording_mbid in candidate.recording_mbids
        or explicit_ids.track_mbid in candidate.track_mbids
    )


def _has_unsafe_text(request: MatchingRequest) -> bool:
    request_text = (request.artist_name, request.release_title)
    lidarr_text = () if request.lidarr is None else (request.lidarr.artist_name, request.lidarr.release_title)
    return any(_contains_control_character(value) for value in (*request_text, *lidarr_text))


def _candidate_has_unsafe_text(candidate: ReleaseCandidate) -> bool:
    return _contains_control_character(candidate.artist_name) or _contains_control_character(candidate.release_title)


def _contains_control_character(value: str) -> bool:
    return any(unicodedata.category(character) in {'Cc', 'Cf', 'Cs'} for character in value)


def _lidarr_conflicts(context: LidarrContext, candidate: ReleaseCandidate) -> bool:
    return _normalized(context.artist_name) != _normalized(candidate.artist_name) or _normalized(
        context.release_title
    ) != _normalized(candidate.release_title)


def _normalized(value: str) -> str:
    decomposed = unicodedata.normalize('NFKD', value).casefold()
    return ''.join(character for character in decomposed if character.isalnum())
