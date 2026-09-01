from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass
from enum import StrEnum
from typing import Final, cast

from rapidfuzz.distance import Levenshtein

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
    release_display_title,
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
MUSICBRAINZ_SCORE_WEIGHT: Final = 0.20
ACOUSTID_SCORE_WEIGHT: Final = 0.40
_TEXT_MATCH_THRESHOLD: Final = 0.85
_CATALOG_TOKEN_PATTERN: Final = re.compile(r'(?i)(?:[a-z]{1,8}[- ]?)?\d(?:[\d-]{3,})')


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
    review_reason: ReviewReason | None
    candidate_scores: tuple[CandidateScore, ...] = ()


def resolve_match(
    request: MatchingRequest,
    musicbrainz: MusicBrainzResult,
    acoustid: AcoustIdResult | None,
    confidence_threshold: float = DEFAULT_CONFIDENCE_THRESHOLD,
) -> MatchResult:
    recording_score = _recording_score(request, acoustid, _recording_candidate(request, musicbrainz))
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
            _recording_score_for_candidate(recording_score, automatic_candidate),
            release_score,
            None,
            candidate_scores,
        )
    automatic_candidate = _unique_confident_match(request, musicbrainz, confidence_threshold)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            _recording_score_for_candidate(recording_score, automatic_candidate),
            score_release_candidate(request, automatic_candidate),
            None,
            candidate_scores,
        )
    automatic_candidate = _unique_recording_context_match(request, musicbrainz)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            _recording_score_for_candidate(recording_score, automatic_candidate),
            score_release_candidate(request, automatic_candidate),
            None,
            candidate_scores,
        )
    automatic_candidate = _unique_catalog_match(request, musicbrainz)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            _recording_score_for_candidate(recording_score, automatic_candidate),
            score_release_candidate(request, automatic_candidate),
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


def _recording_score(
    request: MatchingRequest, acoustid: AcoustIdResult | None, candidate: ReleaseCandidate | None
) -> CandidateScore:
    match acoustid:
        case AcoustIdMatch(evidence=evidence):
            exact_score = 1.0 if request.explicit_ids.recording_mbid == evidence.recording_mbid else evidence.score
            if candidate is None or evidence.recording_mbid not in candidate.recording_mbids:
                return CandidateScore(evidence.recording_mbid, exact_score)
            local_score = _score_recording_candidate(request, candidate)
            provider_scores = (
                ((exact_score, ACOUSTID_SCORE_WEIGHT),)
                if candidate.musicbrainz_score is None
                else (
                    (candidate.musicbrainz_score / 100.0, MUSICBRAINZ_SCORE_WEIGHT),
                    (exact_score, ACOUSTID_SCORE_WEIGHT),
                )
            )
            return _with_provider_scores(local_score, provider_scores)
        case _:
            pass
    if candidate is None:
        return CandidateScore(request.explicit_ids.recording_mbid, 0.0)
    return score_recording_candidate(request, candidate)


def _recording_score_for_candidate(score: CandidateScore, candidate: ReleaseCandidate) -> CandidateScore:
    if not candidate.recording_mbids:
        return score
    return CandidateScore(
        candidate.recording_mbids[0],
        score.score,
        score.artist_component,
        score.release_component,
        score.duration_component,
        score.title_component,
        score.track_component,
    )


def score_recording_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    """Score recording identity using recording artist, title, and duration evidence."""
    if not candidate.recording_mbids:
        return CandidateScore(None, 0.0)
    local_score = _score_recording_candidate(request, candidate)
    return _with_provider_scores(
        local_score,
        ()
        if candidate.musicbrainz_score is None
        else ((candidate.musicbrainz_score / 100.0, MUSICBRAINZ_SCORE_WEIGHT),),
    )


def _score_recording_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    title_component = 0.4 * _text_similarity(request.recording_title, candidate.recording_title or '')
    artist_component = 0.4 * _text_similarity(request.artist_name, _recording_artist_name(candidate))
    duration_component = _duration_score(request.duration_seconds, candidate.duration_seconds)
    return CandidateScore(
        candidate.recording_mbids[0],
        artist_component + title_component + duration_component,
        artist_component,
        0.0,
        duration_component,
        title_component,
    )


def _recording_candidate(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> ReleaseCandidate | None:
    match musicbrainz:
        case MusicBrainzMatch(candidate=candidate) if candidate.recording_mbids:
            return candidate
        case Ambiguous(candidates=candidates):
            matches = tuple(
                candidate
                for candidate in candidates
                if candidate.recording_mbids and recording_candidate_matches(request, candidate)
            )
            return matches[0] if len(matches) == 1 else None
        case _:
            return None


def _recording_artist_name(candidate: ReleaseCandidate) -> str:
    return candidate.recording_artist_names[0] if candidate.recording_artist_names else _release_artist_name(candidate)


def _candidate_scores(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> tuple[CandidateScore, ...]:
    match musicbrainz:
        case MusicBrainzMatch(candidate=candidate):
            return (score_release_candidate(request, candidate),)
        case Ambiguous(candidates=candidates):
            return tuple(score_release_candidate(request, candidate) for candidate in candidates)
        case Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout() | Unavailable():
            return ()


def _unique_source_match(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> ReleaseCandidate | None:
    """Return the sole candidate matching both source artist and album text."""
    if request.explicit_ids.release_mbid or request.explicit_ids.recording_mbid or request.explicit_ids.track_mbid:
        return None
    if not request.artist_name.strip() or not request.release_title.strip() or _has_unsafe_text(request):
        return None
    match musicbrainz:
        case MusicBrainzMatch(provenance=LiveProvenance(state='fresh' | 'cached'), candidate=candidate):
            return candidate if _source_candidate_matches(request, candidate) else None
        case Ambiguous(provenance=LiveProvenance(state='fresh' | 'cached'), candidates=candidates):
            matches = tuple(candidate for candidate in candidates if _source_candidate_matches(request, candidate))
            return matches[0] if len(matches) == 1 else None
        case MusicBrainzMatch() | Ambiguous():
            return None
        case _:
            return None


def _unique_confident_match(
    request: MatchingRequest, musicbrainz: MusicBrainzResult, confidence_threshold: float
) -> ReleaseCandidate | None:
    if (
        request.explicit_ids.release_mbid
        or request.explicit_ids.recording_mbid
        or request.explicit_ids.track_mbid
        or not request.artist_name.strip()
        or _has_unsafe_text(request)
    ):
        return None
    match musicbrainz:
        case MusicBrainzMatch(provenance=LiveProvenance(state='fresh' | 'cached'), candidate=candidate):
            score = score_release_candidate(request, candidate)
            return (
                candidate
                if not _candidate_has_unsafe_text(candidate)
                and score.duration_component > 0.0
                and score.score >= confidence_threshold
                else None
            )
        case Ambiguous(provenance=LiveProvenance(state='fresh' | 'cached'), candidates=candidates):
            qualified = tuple(
                candidate
                for candidate in candidates
                if not _candidate_has_unsafe_text(candidate)
                and (score := score_release_candidate(request, candidate)).duration_component > 0.0
                and score.score >= confidence_threshold
            )
            release_mbids = {candidate.release_mbid for candidate in qualified}
            if len(release_mbids) != 1:
                return None
            return qualified[0]
        case _:
            return None


def _unique_recording_context_match(
    request: MatchingRequest, musicbrainz: MusicBrainzResult
) -> ReleaseCandidate | None:
    if not request.recording_title.strip() or _has_unsafe_text(request):
        return None
    match musicbrainz:
        case MusicBrainzMatch(provenance=LiveProvenance(state='fresh' | 'cached'), candidate=candidate):
            return (
                candidate
                if _has_recording_context(candidate) and recording_candidate_matches(request, candidate)
                else None
            )
        case Ambiguous(provenance=LiveProvenance(state='fresh' | 'cached'), candidates=candidates):
            matches = tuple(
                candidate
                for candidate in candidates
                if _has_recording_context(candidate) and recording_candidate_matches(request, candidate)
            )
            return matches[0] if len(matches) == 1 else None
        case _:
            return None


def _has_recording_context(candidate: ReleaseCandidate) -> bool:
    return candidate.recording_title is not None and candidate.duration_seconds is not None


def _source_candidate_matches(request: MatchingRequest, candidate: ReleaseCandidate) -> bool:
    if _candidate_has_unsafe_text(candidate):
        return False
    artist_name = _album_artist_name(request)
    return _normalized(artist_name) == _normalized(_release_artist_name(candidate)) and _normalized(
        request.release_title
    ) == _normalized(release_display_title(candidate))


def _unique_catalog_match(request: MatchingRequest, musicbrainz: MusicBrainzResult) -> ReleaseCandidate | None:
    path_catalogs = _path_catalog_keys(request.source_path)
    if not path_catalogs or _has_unsafe_text(request):
        return None
    match musicbrainz:
        case Ambiguous(provenance=LiveProvenance(state='fresh' | 'cached'), candidates=candidates):
            matches = tuple(
                candidate
                for candidate in candidates
                if _source_candidate_matches(request, candidate)
                and path_catalogs.intersection(_catalog_keys(candidate.catalog_numbers))
            )
            return matches[0] if len(matches) == 1 else None
        case _:
            return None


def _path_catalog_keys(source_path: str) -> frozenset[str]:
    keys: set[str] = set()
    tokens = cast(list[str], _CATALOG_TOKEN_PATTERN.findall(source_path))
    for token in tokens:
        normalized = _normalized(token)
        keys.add(normalized)
        keys.add(_normalized(re.sub(r'^[a-z]+', '', token, flags=re.IGNORECASE)))
    return frozenset(keys)


def _catalog_keys(catalog_numbers: tuple[str, ...]) -> frozenset[str]:
    return frozenset(_normalized(number) for number in catalog_numbers if number)


def score_release_candidate(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
    if _candidate_matches_explicit_ids(request.explicit_ids, candidate):
        return CandidateScore(candidate.release_mbid, 1.0)
    source_score = _score_components(
        _album_artist_name(request), request.release_title, request.duration_seconds, candidate
    )
    if request.lidarr is None:
        selected = source_score
    else:
        lidarr_score = _score_components(
            request.lidarr.artist_name, request.lidarr.release_title, request.lidarr.duration_seconds, candidate
        )
        selected = source_score if source_score.total >= lidarr_score.total else lidarr_score
    scored = CandidateScore(
        candidate.release_mbid,
        selected.total,
        selected.artist_component,
        selected.release_component,
        selected.duration_component,
        selected.release_component,
    )
    return _with_provider_scores(
        scored,
        ()
        if candidate.musicbrainz_score is None
        else ((candidate.musicbrainz_score / 100.0, MUSICBRAINZ_SCORE_WEIGHT),),
    )


def recording_candidate_matches(request: MatchingRequest, candidate: ReleaseCandidate) -> bool:
    return (
        (
            _text_matches(_album_artist_name(request), candidate.release_artist_name)
            or _text_matches(request.artist_name, candidate.release_artist_name)
            or any(_text_matches(request.artist_name, artist_name) for artist_name in candidate.recording_artist_names)
        )
        and _title_matches(request.recording_title, candidate.recording_title)
        and _duration_matches(request.duration_seconds, candidate.duration_seconds)
    )


def _title_matches(source_title: str, candidate_title: str | None) -> bool:
    return candidate_title is None or (
        bool(source_title)
        and _text_similarity(_recording_title_key(source_title), _recording_title_key(candidate_title))
        >= _TEXT_MATCH_THRESHOLD
    )


def _text_matches(source_value: str, candidate_value: str | None) -> bool:
    return candidate_value is None or _text_similarity(source_value, candidate_value) >= _TEXT_MATCH_THRESHOLD


def _duration_matches(expected: int | None, actual: int | None) -> bool:
    return actual is None or _duration_score(expected, actual) == 0.2


def _recording_title_key(value: str) -> str:
    return _normalized(value).replace('albumversion', '')


def _number_matches(expected: int | None, actual: int | None) -> bool:
    return expected is None or actual is None or expected == actual


def _number_similarity(expected: int | None, actual: int | None) -> float:
    return 0.0 if expected is None or actual is None else float(expected == actual)


def _country_matches(source_path: str, candidate_country: str | None) -> bool:
    return candidate_country is None or 'japan' not in source_path.casefold() or candidate_country == 'JP'


@dataclass(frozen=True, slots=True)
class _ScoreComponents:
    artist_component: float
    release_component: float
    duration_component: float

    @property
    def total(self) -> float:
        return self.artist_component + self.release_component + self.duration_component


def _score_components(
    artist_name: str, release_title: str, duration_seconds: int | None, candidate: ReleaseCandidate
) -> _ScoreComponents:
    scale = 1.0 / 0.6 if not release_title.strip() else 1.0
    return _ScoreComponents(
        artist_component=0.4 * _text_similarity(artist_name, _release_artist_name(candidate)) * scale,
        release_component=0.4 * _text_similarity(release_title, release_display_title(candidate)) * scale,
        duration_component=_duration_score(duration_seconds, candidate.duration_seconds) * scale,
    )


def _release_artist_name(candidate: ReleaseCandidate) -> str:
    return candidate.release_artist_name or candidate.artist_name


def _album_artist_name(request: MatchingRequest) -> str:
    return request.album_artist_name or request.artist_name


def _text_similarity(left: str, right: str) -> float:
    if not left or not right:
        return 0.0
    return Levenshtein.normalized_similarity(_normalized(left), _normalized(right))


def _with_provider_scores(
    local_score: CandidateScore, provider_scores: tuple[tuple[float, float], ...]
) -> CandidateScore:
    provider_weight = sum(weight for _, weight in provider_scores)
    return CandidateScore(
        local_score.candidate_mbid,
        local_score.score * (1.0 - provider_weight) + sum(score * weight for score, weight in provider_scores),
        local_score.artist_component * (1.0 - provider_weight),
        local_score.release_component * (1.0 - provider_weight),
        local_score.duration_component * (1.0 - provider_weight),
        local_score.title_component * (1.0 - provider_weight),
        local_score.track_component * (1.0 - provider_weight),
    )


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
            if (
                not _has_manual_mbid(request.explicit_ids)
                and request.release_title.strip()
                and release_score.release_component < 0.4
            ):
                return ReviewReason.INSUFFICIENT_RELEASE_SCORE
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
    return (
        _text_similarity(context.artist_name, candidate.artist_name) < _TEXT_MATCH_THRESHOLD
        or _text_similarity(context.release_title, candidate.release_title) < _TEXT_MATCH_THRESHOLD
    )


def _normalized(value: str) -> str:
    decomposed = unicodedata.normalize('NFKD', value).casefold()
    return ''.join(character for character in decomposed if character.isalnum())
