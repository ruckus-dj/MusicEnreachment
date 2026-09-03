from __future__ import annotations

import unicodedata
from dataclasses import dataclass
from enum import IntEnum, StrEnum
from typing import Final

from rapidfuzz.distance import Levenshtein

from music_ingest.matching.providers import ReleaseCandidate, release_display_title


class MatchDecision(StrEnum):
    AUTO_SELECTED = 'auto_selected'
    NEEDS_REVIEW = 'needs_review'


DEFAULT_CONFIDENCE_THRESHOLD: Final = 0.70


class ScoreWeight(IntEnum):
    """Relative parts assigned to each available matching factor."""

    ARTIST_SIMILARITY = 4
    RELEASE_TITLE_SIMILARITY = 4
    RECORDING_TITLE_SIMILARITY = 4
    DURATION_SIMILARITY = 2
    MUSICBRAINZ_SEARCH = 2
    ACOUSTID_FINGERPRINT = 4
    TRACK_NUMBER = 2
    DISC_NUMBER = 1
    TRACK_TOTAL = 1
    DISC_TOTAL = 1


class ScoreComponent(StrEnum):
    ARTIST = 'artist'
    RELEASE = 'release'
    DURATION = 'duration'
    TITLE = 'title'
    TRACK = 'track'
    MUSICBRAINZ = 'musicbrainz'
    ACOUSTID = 'acoustid'


@dataclass(frozen=True, slots=True)
class _ScoreFactor:
    component: ScoreComponent
    value: float
    weight: ScoreWeight
    available: bool


def _weighted_score(factors: tuple[_ScoreFactor, ...]) -> tuple[float, int]:
    total_weight = sum(factor.weight for factor in factors if factor.available)
    score = sum(factor.value * factor.weight for factor in factors if factor.available)
    return (score / total_weight if total_weight else 0.0), total_weight


def _component_score(factors: tuple[_ScoreFactor, ...], component: ScoreComponent, total_weight: int) -> float:
    return sum(
        factor.value * factor.weight / total_weight
        for factor in factors
        if factor.available and factor.component is component and total_weight
    )


@dataclass(frozen=True, slots=True)
class ExplicitMusicBrainzIds:
    release_mbid: str | None = None
    recording_mbid: str | None = None
    track_mbid: str | None = None


@dataclass(frozen=True, slots=True)
class MatchingRequest:
    artist_name: str
    release_title: str
    duration_seconds: int | None
    explicit_ids: ExplicitMusicBrainzIds = ExplicitMusicBrainzIds()
    recording_title: str = ''
    track_number: int | None = None
    track_total: int | None = None
    disc_number: int | None = None
    disc_total: int | None = None
    source_path: str = ''
    album_artist_name: str = ''


@dataclass(frozen=True, slots=True)
class CandidateScore:
    candidate_mbid: str | None
    score: float
    artist_component: float = 0.0
    release_component: float = 0.0
    duration_component: float = 0.0
    title_component: float = 0.0
    track_component: float = 0.0
    weight: int = 0


def select_folder_release(
    candidate_scores: tuple[tuple[CandidateScore, ...], ...], confidence_threshold: float = 0.0
) -> str | None:
    """Select the highest-scoring release shared by every qualified source group."""
    groups = tuple(
        tuple(score for score in group if score.candidate_mbid is not None and score.score >= confidence_threshold)
        for group in candidate_scores
    )
    if not groups or any(not group for group in groups):
        return None
    shared_mbids = set(score.candidate_mbid for score in groups[0] if score.candidate_mbid is not None)
    for group in groups[1:]:
        shared_mbids.intersection_update(score.candidate_mbid for score in group if score.candidate_mbid is not None)
    if not shared_mbids:
        return None
    totals = {
        mbid: sum(score.score for group in groups for score in group if score.candidate_mbid == mbid)
        for mbid in shared_mbids
    }
    return min(shared_mbids, key=lambda mbid: (-totals[mbid], mbid))


@dataclass(frozen=True, slots=True)
class MatchResult:
    decision: MatchDecision
    selected_release_mbid: str | None
    recording_score: CandidateScore
    release_score: CandidateScore
    review_reason: str | None
    candidate_scores: tuple[CandidateScore, ...] = ()


def score_recording_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    """Score recording identity using recording artist, title, and duration evidence."""
    if not candidate.recording_mbids:
        return CandidateScore(None, 0.0)
    return _score_candidate(candidate.recording_mbids[0], _recording_factors(request, candidate))


def _recording_factors(
    request: MatchingRequest, candidate: ReleaseCandidate, acoustid_score: float | None = None
) -> tuple[_ScoreFactor, ...]:
    candidate_title = candidate.recording_title or ''
    recording_artist = _recording_artist_name(candidate)
    factors = (
        _ScoreFactor(
            ScoreComponent.TITLE,
            _text_similarity(request.recording_title, candidate_title),
            ScoreWeight.RECORDING_TITLE_SIMILARITY,
            bool(request.recording_title.strip() and candidate_title.strip()),
        ),
        _ScoreFactor(
            ScoreComponent.ARTIST,
            _text_similarity(request.artist_name, recording_artist),
            ScoreWeight.ARTIST_SIMILARITY,
            bool(request.artist_name.strip() and recording_artist.strip()),
        ),
        _ScoreFactor(
            ScoreComponent.DURATION,
            _duration_score(request.duration_seconds, candidate.duration_seconds),
            ScoreWeight.DURATION_SIMILARITY,
            request.duration_seconds is not None and candidate.duration_seconds is not None,
        ),
    )
    if candidate.musicbrainz_score is not None:
        factors += (
            _ScoreFactor(
                ScoreComponent.MUSICBRAINZ,
                candidate.musicbrainz_score / 100.0,
                ScoreWeight.MUSICBRAINZ_SEARCH,
                True,
            ),
        )
    if acoustid_score is not None:
        factors += (_ScoreFactor(ScoreComponent.ACOUSTID, acoustid_score, ScoreWeight.ACOUSTID_FINGERPRINT, True),)
    return factors


def _score_candidate(candidate_mbid: str | None, factors: tuple[_ScoreFactor, ...]) -> CandidateScore:
    score, total_weight = _weighted_score(factors)
    return CandidateScore(
        candidate_mbid,
        score,
        _component_score(factors, ScoreComponent.ARTIST, total_weight),
        _component_score(factors, ScoreComponent.RELEASE, total_weight),
        _component_score(factors, ScoreComponent.DURATION, total_weight),
        _component_score(factors, ScoreComponent.TITLE, total_weight),
        _component_score(factors, ScoreComponent.TRACK, total_weight),
        total_weight,
    )


def _recording_artist_name(candidate: ReleaseCandidate) -> str:
    return candidate.recording_artist_names[0] if candidate.recording_artist_names else _release_artist_name(candidate)


def score_release_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    if _candidate_matches_explicit_ids(request.explicit_ids, candidate):
        return CandidateScore(candidate.release_mbid, 1.0)
    source_factors = _release_factors(
        _album_artist_name(request), request.release_title, request.duration_seconds, candidate
    )
    position_factors = (
        _position_factor(request.track_number, candidate.track_number, ScoreWeight.TRACK_NUMBER),
        _position_factor(request.disc_number, candidate.disc_number, ScoreWeight.DISC_NUMBER),
        _position_factor(request.track_total, candidate.track_total, ScoreWeight.TRACK_TOTAL),
        _position_factor(request.disc_total, candidate.disc_total, ScoreWeight.DISC_TOTAL),
    )
    return _score_candidate(candidate.release_mbid, source_factors + position_factors)


def _position_factor(expected: int | None, actual: int | None, weight: ScoreWeight) -> _ScoreFactor:
    comparable = expected is not None and expected > 0 and actual is not None and actual > 0
    return _ScoreFactor(
        ScoreComponent.TRACK,
        1.0 if comparable and expected == actual else 0.0,
        weight,
        comparable,
    )


def _release_factors(
    artist_name: str, release_title: str, duration_seconds: int | None, candidate: ReleaseCandidate
) -> tuple[_ScoreFactor, ...]:
    candidate_artist = _release_artist_name(candidate)
    candidate_title = release_display_title(candidate)
    factors = (
        _ScoreFactor(
            ScoreComponent.ARTIST,
            _text_similarity(artist_name, candidate_artist),
            ScoreWeight.ARTIST_SIMILARITY,
            bool(artist_name.strip() and candidate_artist.strip()),
        ),
        _ScoreFactor(
            ScoreComponent.RELEASE,
            _text_similarity(release_title, candidate_title),
            ScoreWeight.RELEASE_TITLE_SIMILARITY,
            bool(release_title.strip() and candidate_title.strip()),
        ),
        _ScoreFactor(
            ScoreComponent.DURATION,
            _duration_score(duration_seconds, candidate.duration_seconds),
            ScoreWeight.DURATION_SIMILARITY,
            duration_seconds is not None and candidate.duration_seconds is not None,
        ),
    )
    if candidate.musicbrainz_score is not None:
        factors += (
            _ScoreFactor(
                ScoreComponent.MUSICBRAINZ,
                candidate.musicbrainz_score / 100.0,
                ScoreWeight.MUSICBRAINZ_SEARCH,
                True,
            ),
        )
    return factors


def _release_artist_name(candidate: ReleaseCandidate) -> str:
    return candidate.release_artist_name or candidate.artist_name


def _album_artist_name(request: MatchingRequest) -> str:
    return request.album_artist_name or request.artist_name


def _text_similarity(left: str, right: str) -> float:
    if not left or not right:
        return 0.0
    return Levenshtein.normalized_similarity(_normalized(left), _normalized(right))


def _duration_score(expected: int | None, actual: int | None) -> float:
    if expected is None or actual is None:
        return 0.0
    difference = abs(expected - actual)
    if difference <= 2:
        return 1.0
    if difference <= 5:
        return 0.5
    return 0.0


def _candidate_matches_explicit_ids(explicit_ids: ExplicitMusicBrainzIds, candidate: ReleaseCandidate) -> bool:
    return (
        explicit_ids.release_mbid == candidate.release_mbid
        or explicit_ids.recording_mbid in candidate.recording_mbids
        or explicit_ids.track_mbid in candidate.track_mbids
    )


def _normalized(value: str) -> str:
    decomposed = unicodedata.normalize('NFKD', value).casefold()
    return ''.join(character for character in decomposed if character.isalnum())
