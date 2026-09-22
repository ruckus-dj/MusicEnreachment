from __future__ import annotations

from dataclasses import dataclass
from enum import IntEnum, StrEnum
from math import log2, pow
from typing import Final

from rapidfuzz import fuzz
from rapidfuzz.utils import default_process
from unidecode import unidecode

from music_ingest.services.matching.providers import ReleaseCandidate, release_display_title


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
    RECORDING_ARTIST = 'recording_artist'
    RELEASE_ARTIST = 'release_artist'
    RELEASE = 'release'
    DURATION = 'duration'
    TITLE = 'title'
    TRACK = 'track'
    MUSICBRAINZ = 'musicbrainz'
    ACOUSTID = 'acoustid'
    TRACK_NUMBER = 'track_number'
    TRACK_TOTAL = 'track_total'
    DISC_NUMBER = 'disc_number'
    DISC_TOTAL = 'disc_total'


_POSITION_COMPONENTS: Final = frozenset(
    {ScoreComponent.TRACK_NUMBER, ScoreComponent.TRACK_TOTAL, ScoreComponent.DISC_NUMBER, ScoreComponent.DISC_TOTAL}
)


@dataclass(frozen=True, slots=True)
class _ScoreFactor:
    component: ScoreComponent
    value: float
    weight: ScoreWeight
    available: bool


def _components_for(component: ScoreComponent) -> frozenset[ScoreComponent]:
    """The factor components that roll up into a given reported component."""
    if component is ScoreComponent.TRACK:
        return _POSITION_COMPONENTS
    if component is ScoreComponent.ARTIST:
        return frozenset({ScoreComponent.RECORDING_ARTIST, ScoreComponent.RELEASE_ARTIST})
    return frozenset({component})


def _weighted_average(factors: tuple[_ScoreFactor, ...]) -> float | None:
    """The weight-averaged value of the given factors, or None if they carry no weight."""
    weight = sum(factor.weight for factor in factors)
    return sum(factor.value * factor.weight for factor in factors) / weight if weight else None


def _weighted_score(factors: tuple[_ScoreFactor, ...]) -> tuple[float, int]:
    available = tuple(factor for factor in factors if factor.available)
    total_weight = sum(factor.weight for factor in available)
    average = _weighted_average(available)
    return (max(0.0, average) if average is not None else 0.0), total_weight


def _component_score(factors: tuple[_ScoreFactor, ...], component: ScoreComponent, total_weight: int) -> float:
    if not total_weight:
        return 0.0
    components = _components_for(component)
    matching = tuple(factor for factor in factors if factor.available and factor.component in components)
    return sum(factor.value * factor.weight for factor in matching) / total_weight


def _component_match(factors: tuple[_ScoreFactor, ...], component: ScoreComponent) -> float | None:
    components = _components_for(component)
    matching = tuple(factor for factor in factors if factor.available and factor.component in components)
    return _weighted_average(matching)


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
    track_number_component: float | None = None
    track_total_component: float | None = None
    disc_number_component: float | None = None
    disc_total_component: float | None = None
    musicbrainz_component: float | None = None
    acoustid_component: float | None = None
    artist_match: float | None = None
    release_match: float | None = None
    duration_match: float | None = None
    title_match: float | None = None
    track_number_match: float | None = None
    track_total_match: float | None = None
    disc_number_match: float | None = None
    disc_total_match: float | None = None
    musicbrainz_match: float | None = None
    acoustid_match: float | None = None
    recording_artist_component: float | None = None
    release_artist_component: float | None = None
    recording_artist_match: float | None = None
    release_artist_match: float | None = None


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


def score_recording_candidate(
    request: MatchingRequest, candidate: ReleaseCandidate, acoustid_score: float | None = None
) -> CandidateScore:
    """Score recording identity using metadata, provider, and optional fingerprint evidence."""
    if not candidate.recording_mbids:
        return CandidateScore(None, 0.0)
    return _score_candidate(candidate.recording_mbids[0], _recording_factors(request, candidate, acoustid_score))


def score_recording_release_candidate(
    request: MatchingRequest, candidate: ReleaseCandidate, acoustid_score: float | None = None
) -> CandidateScore:
    """Score one recording and its release as a single MusicBrainz candidate."""
    if not candidate.recording_mbids:
        return CandidateScore(None, 0.0)
    if _candidate_matches_explicit_ids(request.explicit_ids, candidate):
        return CandidateScore(candidate.recording_mbids[0], 1.0)
    return _score_candidate(
        candidate.recording_mbids[0],
        _recording_factors(request, candidate, acoustid_score)
        + _release_factors(_album_artist_name(request), request.release_title, request.duration_seconds, candidate)
        + (
            _position_factor(
                request.track_number, candidate.track_number, ScoreWeight.TRACK_NUMBER, ScoreComponent.TRACK_NUMBER
            ),
            _position_factor(
                request.disc_number, candidate.disc_number, ScoreWeight.DISC_NUMBER, ScoreComponent.DISC_NUMBER
            ),
            _position_factor(
                request.track_total, candidate.track_total, ScoreWeight.TRACK_TOTAL, ScoreComponent.TRACK_TOTAL
            ),
            _position_factor(
                request.disc_total, candidate.disc_total, ScoreWeight.DISC_TOTAL, ScoreComponent.DISC_TOTAL
            ),
        ),
    )


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
            ScoreComponent.RECORDING_ARTIST,
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
        _component_score(factors, ScoreComponent.TRACK_NUMBER, total_weight),
        _component_score(factors, ScoreComponent.TRACK_TOTAL, total_weight),
        _component_score(factors, ScoreComponent.DISC_NUMBER, total_weight),
        _component_score(factors, ScoreComponent.DISC_TOTAL, total_weight),
        _component_score(factors, ScoreComponent.MUSICBRAINZ, total_weight),
        _component_score(factors, ScoreComponent.ACOUSTID, total_weight),
        _component_match(factors, ScoreComponent.ARTIST),
        _component_match(factors, ScoreComponent.RELEASE),
        _component_match(factors, ScoreComponent.DURATION),
        _component_match(factors, ScoreComponent.TITLE),
        _component_match(factors, ScoreComponent.TRACK_NUMBER),
        _component_match(factors, ScoreComponent.TRACK_TOTAL),
        _component_match(factors, ScoreComponent.DISC_NUMBER),
        _component_match(factors, ScoreComponent.DISC_TOTAL),
        _component_match(factors, ScoreComponent.MUSICBRAINZ),
        _component_match(factors, ScoreComponent.ACOUSTID),
        _component_score(factors, ScoreComponent.RECORDING_ARTIST, total_weight),
        _component_score(factors, ScoreComponent.RELEASE_ARTIST, total_weight),
        _component_match(factors, ScoreComponent.RECORDING_ARTIST),
        _component_match(factors, ScoreComponent.RELEASE_ARTIST),
    )


def _recording_artist_name(candidate: ReleaseCandidate) -> str:
    return '; '.join(candidate.recording_artist_names) or _release_artist_name(candidate)


def score_release_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    if _candidate_matches_explicit_ids(request.explicit_ids, candidate):
        return CandidateScore(candidate.release_mbid, 1.0)
    source_factors = _release_factors(
        _album_artist_name(request), request.release_title, request.duration_seconds, candidate
    )
    position_factors = (
        _position_factor(
            request.track_number, candidate.track_number, ScoreWeight.TRACK_NUMBER, ScoreComponent.TRACK_NUMBER
        ),
        _position_factor(
            request.disc_number, candidate.disc_number, ScoreWeight.DISC_NUMBER, ScoreComponent.DISC_NUMBER
        ),
        _position_factor(
            request.track_total, candidate.track_total, ScoreWeight.TRACK_TOTAL, ScoreComponent.TRACK_TOTAL
        ),
        _position_factor(request.disc_total, candidate.disc_total, ScoreWeight.DISC_TOTAL, ScoreComponent.DISC_TOTAL),
    )
    return _score_candidate(candidate.release_mbid, source_factors + position_factors)


def _position_factor(
    expected: int | None, actual: int | None, weight: ScoreWeight, component: ScoreComponent
) -> _ScoreFactor:
    comparable = expected is not None and expected > 0 and actual is not None and actual > 0
    return _ScoreFactor(
        component,
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
            ScoreComponent.RELEASE_ARTIST,
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
    return text_similarity(left, right)


def text_similarity(left: str, right: str) -> float:
    """Compare two music metadata strings using the normalization used by candidate matching."""
    if not left or not right:
        return 0.0
    original_similarity = _soft_token_ratio(left, right)
    transliterated_similarity = _soft_token_ratio(unidecode(left), unidecode(right))
    return max(original_similarity, transliterated_similarity)


def _soft_token_ratio(left: str, right: str) -> float:
    left_tokens = _normalized(left).split()
    right_tokens = _normalized(right).split()
    if not left_tokens or not right_tokens:
        return 0.0
    normalized_left = ' '.join(left_tokens)
    normalized_right = ' '.join(right_tokens)
    token_coverage = min(len(left_tokens), len(right_tokens)) / max(len(left_tokens), len(right_tokens))
    character_coverage = min(len(normalized_left), len(normalized_right)) / max(
        len(normalized_left), len(normalized_right)
    )
    coverage_penalty = 0.6 + 0.4 * ((token_coverage + character_coverage) / 2)
    return fuzz.token_set_ratio(normalized_left, normalized_right) / 100.0 * coverage_penalty


def _duration_score(expected: int | None, actual: int | None) -> float:
    if expected is None or actual is None or expected <= 0 or actual <= 0:
        return 0.0
    difference = abs(expected - actual)
    local_similarity = pow(max(0.0, 1.0 - difference / 20.0), 2.2)
    duration_ratio = max(expected, actual) / min(expected, actual)
    return local_similarity - log2(duration_ratio)


def _candidate_matches_explicit_ids(explicit_ids: ExplicitMusicBrainzIds, candidate: ReleaseCandidate) -> bool:
    return (
        explicit_ids.release_mbid == candidate.release_mbid
        or explicit_ids.recording_mbid in candidate.recording_mbids
        or explicit_ids.track_mbid in candidate.track_mbids
    )


def _normalized(value: str) -> str:
    return default_process(value)
