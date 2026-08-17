from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass
from enum import StrEnum
from typing import Final

from rapidfuzz.fuzz import ratio

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
    automatic_candidate = _unique_recording_context_match(request, musicbrainz)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            recording_score,
            _candidate_score_result(request, automatic_candidate),
            None,
            candidate_scores,
        )
    automatic_candidate = _unique_catalog_match(request, musicbrainz)
    if automatic_candidate is not None:
        return MatchResult(
            MatchDecision.AUTO_SELECTED,
            automatic_candidate.release_mbid,
            recording_score,
            _candidate_score_result(request, automatic_candidate),
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
            return (_candidate_score_result(request, candidate),)
        case Ambiguous(candidates=candidates):
            return tuple(_candidate_score_result(request, candidate) for candidate in candidates)
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
    ) == _normalized(candidate.release_title)


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
    for token in _CATALOG_TOKEN_PATTERN.findall(source_path):
        normalized = _normalized(token)
        keys.add(normalized)
        keys.add(_normalized(re.sub(r'^[a-z]+', '', token, flags=re.IGNORECASE)))
    return frozenset(keys)


def _catalog_keys(catalog_numbers: tuple[str, ...]) -> frozenset[str]:
    return frozenset(_normalized(number) for number in catalog_numbers if number)


def _candidate_score_result(request: MatchingRequest, candidate: ReleaseCandidate) -> CandidateScore:
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
    return CandidateScore(
        candidate.release_mbid,
        selected.total,
        selected.artist_component,
        selected.release_component,
        selected.duration_component,
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
        and _number_matches(request.track_number, candidate.track_number)
        and _number_matches(request.track_total, candidate.track_total)
        and _number_matches(request.disc_number, candidate.disc_number)
        and _number_matches(request.disc_total, candidate.disc_total)
        and _country_matches(request.source_path, candidate.country)
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
    return _ScoreComponents(
        artist_component=0.4 * _text_similarity(artist_name, _release_artist_name(candidate)),
        release_component=0.4 * _text_similarity(release_title, candidate.release_title),
        duration_component=_duration_score(duration_seconds, candidate.duration_seconds),
    )


def _release_artist_name(candidate: ReleaseCandidate) -> str:
    return candidate.release_artist_name or candidate.artist_name


def _album_artist_name(request: MatchingRequest) -> str:
    return request.album_artist_name or request.artist_name


def _text_similarity(left: str, right: str) -> float:
    if not left or not right:
        return 0.0
    return ratio(_normalized(left), _normalized(right)) / 100.0


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
            if not _has_manual_mbid(request.explicit_ids) and release_score.release_component < 0.4:
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
