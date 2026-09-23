from __future__ import annotations

import json
from collections.abc import Callable, Iterable, Sequence
from pathlib import Path

from pydantic import TypeAdapter

from music_ingest.contracts import CandidateEvidencePayload
from music_ingest.models import (
    CandidateRecord,
    LibraryRecord,
    ProviderCandidateRunRecord,
    SourceRecord,
)
from music_ingest.services.matching.evidence import ProviderEvidenceResult
from music_ingest.services.matching.providers import (
    ReleaseCandidate,
    release_display_title,
)
from music_ingest.services.matching.scoring import (
    CandidateScore,
    ExplicitMusicBrainzIds,
    MatchDecision,
    MatchingRequest,
    MatchResult,
    score_recording_release_candidate,
)
from music_ingest.services.musicbrainz_identity import ConfirmedMusicBrainzIdentity
from music_ingest.services.normalize.genre_names import display_genre_name

_TAGS_ADAPTER = TypeAdapter(dict[str, str])


def _candidate_tags(candidate: ReleaseCandidate) -> dict[str, str]:
    tags: dict[str, str] = {
        'ALBUM': release_display_title(candidate),
        'ARTIST': '; '.join(candidate.recording_artist_names) or candidate.artist_name,
        'ALBUMARTIST': (
            '; '.join(candidate.release_artist_names) or candidate.release_artist_name or candidate.artist_name
        ),
        'MUSICBRAINZ_ALBUMID': candidate.release_mbid,
    }
    if candidate.recording_mbids:
        tags['MUSICBRAINZ_RECORDINGID'] = candidate.recording_mbids[0]
    optional_tags = {
        'TITLE': candidate.recording_title,
        'DATE': candidate.date,
        'ORIGINALDATE': candidate.original_date,
        'TRACKNUMBER': None if candidate.track_number is None else str(candidate.track_number),
        'TRACKTOTAL': None if candidate.track_total is None else str(candidate.track_total),
        'DISCNUMBER': None if candidate.disc_number is None else str(candidate.disc_number),
        'DISCTOTAL': None if candidate.disc_total is None else str(candidate.disc_total),
        'GENRE': '; '.join(display_genre_name(genre) for genre in candidate.genres) if candidate.genres else None,
        'ISRC': '; '.join(candidate.isrcs) if candidate.isrcs else None,
        'PERFORMER': '; '.join(candidate.performers) if candidate.performers else None,
        'MUSICBRAINZ_ARTISTID': '; '.join(candidate.recording_artist_mbids) or None,
        'MUSICBRAINZ_ALBUMARTISTID': '; '.join(candidate.release_artist_mbids) or None,
        'MUSICBRAINZ_RELEASEGROUPID': candidate.release_group_mbid,
    }
    tags.update({name: value for name, value in optional_tags.items() if value is not None})
    return tags


def musicbrainz_pair_tags(candidate: ReleaseCandidate, identity: ConfirmedMusicBrainzIdentity) -> dict[str, str] | None:
    if candidate.release_mbid != identity.release_mbid:
        return None
    recording_candidates = candidate.recording_candidates or (candidate,)
    matching = next(
        (item for item in recording_candidates if identity.recording_mbid in item.recording_mbids),
        None,
    )
    if matching is None:
        return None
    return {**_candidate_tags(matching), 'MUSICBRAINZ_RECORDINGID': identity.recording_mbid}


def _score_components_evidence(unified_score: CandidateScore) -> dict[str, float | bool | None]:
    return {
        'artist': unified_score.artist_component,
        'release': unified_score.release_component,
        'title': unified_score.title_component,
        'duration': unified_score.duration_component,
        'track': unified_score.track_component,
        'track_number': unified_score.track_number_component,
        'track_total': unified_score.track_total_component,
        'disc_number': unified_score.disc_number_component,
        'disc_total': unified_score.disc_total_component,
        'musicbrainz': unified_score.musicbrainz_component,
        'acoustid': unified_score.acoustid_component,
        'artist_match': unified_score.artist_match,
        'release_match': unified_score.release_match,
        'duration_match': unified_score.duration_match,
        'title_match': unified_score.title_match,
        'track_number_match': unified_score.track_number_match,
        'track_total_match': unified_score.track_total_match,
        'disc_number_match': unified_score.disc_number_match,
        'disc_total_match': unified_score.disc_total_match,
        'musicbrainz_match': unified_score.musicbrainz_match,
        'acoustid_match': unified_score.acoustid_match,
        'recording_artist': unified_score.recording_artist_component,
        'release_artist': unified_score.release_artist_component,
        'recording_artist_match': unified_score.recording_artist_match,
        'release_artist_match': unified_score.release_artist_match,
    }


def _candidate_evidence(
    recording_candidate: ReleaseCandidate,
    recording_mbid: str,
    request: MatchingRequest | None,
    source: SourceRecord | None,
) -> dict[str, object]:
    unified_score = (
        score_recording_release_candidate(
            request,
            recording_candidate,
            None if source is None else _acoustid_recording_score(source, recording_mbid),
        )
        if request is not None
        else None
    )
    return {
        'provider': 'musicbrainz',
        'entity': 'recording_release',
        'artist': '; '.join(recording_candidate.recording_artist_names) or recording_candidate.artist_name,
        'release': recording_candidate.release_title,
        'title': recording_candidate.recording_title or '',
        'album': recording_candidate.release_title,
        'score': None if unified_score is None else unified_score.score,
        'duration_seconds': recording_candidate.duration_seconds,
        'musicbrainz_score': None
        if recording_candidate.musicbrainz_score is None
        else recording_candidate.musicbrainz_score / 100.0,
        'score_components': None if unified_score is None else _score_components_evidence(unified_score),
        'release_mbid': recording_candidate.release_mbid,
        'recording_mbid': recording_mbid,
        'compatible_ids': (),
        'tags': _candidate_tags(recording_candidate),
    }


def _candidate_records(
    source_id: str,
    candidate: ReleaseCandidate,
    _release_score: CandidateScore | None,
    request: MatchingRequest | None,
    source: SourceRecord | None = None,
) -> tuple[CandidateRecord, ...]:
    recording_candidates = candidate.recording_candidates or (candidate,)
    records: list[CandidateRecord] = []
    for recording_candidate in recording_candidates:
        for recording_mbid in recording_candidate.recording_mbids:
            evidence = _candidate_evidence(recording_candidate, recording_mbid, request, source)
            records.append(
                CandidateRecord(
                    source_id=source_id,
                    candidate_key=f'{recording_candidate.release_mbid}:{recording_mbid}',
                    evidence=json.dumps(evidence, sort_keys=True),
                )
            )
    return tuple(records)


def release_candidate_records(source_id: str, candidate: ReleaseCandidate) -> tuple[CandidateRecord, ...]:
    return _candidate_records(source_id, candidate, None, None)


def _latest_candidate_run(source: SourceRecord, provider: str) -> ProviderCandidateRunRecord | None:
    runs = tuple(run for run in source.candidate_runs if run.provider_name == provider)
    return None if not runs else max(runs, key=lambda run: run.id)


def _acoustid_recording_mbids(source: SourceRecord) -> tuple[str, ...]:
    recording_mbids: list[str] = []
    for candidate in reversed(source.candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'acoustid':
            recording_mbid = evidence.tags.get('MUSICBRAINZ_RECORDINGID') or evidence.tags.get('MUSICBRAINZ_TRACKID')
            if recording_mbid is not None and recording_mbid not in recording_mbids:
                recording_mbids.append(recording_mbid)
    return tuple(recording_mbids)


def _acoustid_recording_score(source: SourceRecord, recording_mbid: str) -> float | None:
    for candidate in reversed(source.candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if (
            evidence.provider == 'acoustid'
            and (evidence.tags.get('MUSICBRAINZ_RECORDINGID') or evidence.tags.get('MUSICBRAINZ_TRACKID'))
            == recording_mbid
        ):
            return evidence.score
    return None


def _unique_top_scored[T](items: Sequence[T], score: Callable[[T], float]) -> T | None:
    """Return the single item with the highest score, or None if no items or the top score ties."""
    if not items:
        return None
    best_score = max(score(item) for item in items)
    leaders = tuple(item for item in items if score(item) == best_score)
    return leaders[0] if len(leaders) == 1 else None


def _single_qualified[T](items: Sequence[T]) -> T | None:
    """Return the one item that qualified, or None if zero or more than one did."""
    return items[0] if len(items) == 1 else None


def _single_scored_candidate(
    source: SourceRecord, provider: str, confidence_threshold: float
) -> tuple[str, CandidateEvidencePayload] | None:
    candidates_by_key: dict[str, CandidateEvidencePayload] = {}
    latest_run = _latest_candidate_run(source, provider)
    candidates = source.candidates if latest_run is None else latest_run.candidates
    for candidate in reversed(candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if (
            evidence.provider == provider
            and not (provider == 'musicbrainz' and evidence.entity != 'recording_release')
            and candidate.candidate_key not in candidates_by_key
        ):
            candidates_by_key[candidate.candidate_key] = evidence
    qualified = tuple(
        (candidate_key, evidence)
        for candidate_key, evidence in candidates_by_key.items()
        if evidence.score is not None and evidence.score >= confidence_threshold
    )

    def _candidate_score(candidate: tuple[str, CandidateEvidencePayload]) -> float:
        return candidate[1].score or 0.0

    return _unique_top_scored(qualified, _candidate_score)


def _single_scored_recording_candidate(
    source: SourceRecord, confidence_threshold: float, preferred_recording_mbid: str | None = None
) -> tuple[str, CandidateEvidencePayload] | None:
    candidates_by_recording: dict[str, tuple[float, CandidateEvidencePayload]] = {}
    for provider in ('acoustid', 'musicbrainz'):
        latest_run = _latest_candidate_run(source, provider)
        candidates = source.candidates if latest_run is None else latest_run.candidates
        for candidate in reversed(candidates):
            evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
            if (
                evidence.provider != provider
                or evidence.entity not in {'recording', 'recording_release'}
                or evidence.score is None
            ):
                continue
            recording_mbid = (
                evidence.recording_mbid
                or evidence.tags.get('MUSICBRAINZ_RECORDINGID')
                or evidence.tags.get('MUSICBRAINZ_TRACKID')
                or candidate.candidate_key
            )
            current = candidates_by_recording.get(recording_mbid)
            if (
                current is None
                or (current[1].score_components is None and evidence.score_components is not None)
                or (
                    (current[1].score_components is None) == (evidence.score_components is None)
                    and current[0] < evidence.score
                )
            ):
                candidates_by_recording[recording_mbid] = (evidence.score, evidence)
    composite_candidates = {
        recording_mbid: candidate
        for recording_mbid, candidate in candidates_by_recording.items()
        if candidate[1].score_components is not None
    }
    ranked_candidates = composite_candidates or candidates_by_recording
    if preferred_recording_mbid is not None:
        preferred = ranked_candidates.get(preferred_recording_mbid)
        if preferred is not None and preferred[0] >= confidence_threshold:
            return preferred_recording_mbid, preferred[1]
    qualified = tuple(
        (recording_mbid, candidate[1])
        for recording_mbid, candidate in ranked_candidates.items()
        if candidate[0] >= confidence_threshold
    )
    return _single_qualified(qualified)


def _stored_match_tags(
    recording: tuple[str, CandidateEvidencePayload] | None,
    release: tuple[str, CandidateEvidencePayload] | None,
) -> dict[str, str]:
    if release is None:
        return {}
    if recording is None:
        release_key, release_evidence = release
        release_mbid = release_evidence.release_mbid or release_key.split(':', 1)[0]
        return {**release_evidence.tags, 'MUSICBRAINZ_ALBUMID': release_mbid}
    recording_mbid, _ = recording
    release_key, release_evidence = release
    release_mbid = release_evidence.release_mbid or release_key.split(':', 1)[0]
    release_recording_mbid = release_evidence.tags.get('MUSICBRAINZ_RECORDINGID') or release_evidence.tags.get(
        'MUSICBRAINZ_TRACKID'
    )
    if release_recording_mbid != recording_mbid:
        return {}
    return {
        **release_evidence.tags,
        'MUSICBRAINZ_ALBUMID': release_mbid,
        'MUSICBRAINZ_RECORDINGID': recording_mbid,
    }


def _stored_release_recording_mbid(release: tuple[str, CandidateEvidencePayload] | None) -> str | None:
    if release is None:
        return None
    evidence = release[1]
    return (
        evidence.tags.get('MUSICBRAINZ_RECORDINGID')
        or evidence.tags.get('MUSICBRAINZ_TRACKID')
        or (evidence.compatible_ids[0] if len(evidence.compatible_ids) == 1 else None)
    )


def _release_candidates_for_recording(
    recording_mbid: str, candidates: Iterable[ReleaseCandidate]
) -> tuple[ReleaseCandidate, ...]:
    return tuple(candidate for candidate in candidates if recording_mbid in candidate.recording_mbids)


def _unique_acoustid_album_match(
    candidate_matches: tuple[tuple[ProviderEvidenceResult, MatchResult], ...],
) -> tuple[ProviderEvidenceResult, MatchResult] | None:
    automatic_matches = tuple(
        candidate_match
        for candidate_match in candidate_matches
        if candidate_match[1].decision is MatchDecision.AUTO_SELECTED
    )

    def _match_score(match: tuple[ProviderEvidenceResult, MatchResult]) -> float:
        return match[1].release_score.score

    return _unique_top_scored(automatic_matches, _match_score)


def _unique_acoustid_recording_match(
    candidate_matches: tuple[tuple[ProviderEvidenceResult, CandidateScore], ...],
    confidence_threshold: float,
) -> tuple[ProviderEvidenceResult, CandidateScore] | None:
    qualified_matches = tuple(
        candidate_match for candidate_match in candidate_matches if candidate_match[1].score >= confidence_threshold
    )
    return _single_qualified(qualified_matches)


def select_acoustid_recording_match(
    album_matches: tuple[tuple[ProviderEvidenceResult, MatchResult], ...],
    recording_matches: tuple[tuple[ProviderEvidenceResult, CandidateScore], ...],
    confidence_threshold: float,
) -> tuple[ProviderEvidenceResult, CandidateScore] | None:
    selected_album_match = _unique_acoustid_album_match(album_matches)
    if selected_album_match is not None:
        provider_result, album_match = selected_album_match
        selected_recording_matches = tuple(
            recording_match for recording_match in recording_matches if recording_match[0] == provider_result
        )
        if len(selected_recording_matches) == 1:
            return selected_recording_matches[0]
        album_recording = album_match.recording_score
        if album_recording.candidate_mbid is not None and album_recording.score >= confidence_threshold:
            return provider_result, album_recording
        return None
    return _unique_acoustid_recording_match(recording_matches, confidence_threshold)


def _acoustid_recording_mbid(source: SourceRecord) -> str | None:
    recording_mbids = _acoustid_recording_mbids(source)
    return recording_mbids[0] if recording_mbids else None


def _has_reviewer_decision(source: SourceRecord, state: str) -> bool:
    return any(decision.state == state for decision in source.review_decisions)


def musicbrainz_lookup_ids(record: LibraryRecord, source: SourceRecord) -> tuple[str | None, str | None]:
    recording_mbid = (
        source.association_override.recording_mbid
        if source.association_override is not None
        else (
            record.musicbrainz_recording_id
            if _has_reviewer_decision(source, 'acoustid_confirmed')
            or _has_reviewer_decision(source, 'recording_confirmed')
            else _acoustid_recording_mbid(source)
        )
    )
    release_mbid = record.musicbrainz_release_id if _has_reviewer_decision(source, 'confirmed') else None
    return recording_mbid, release_mbid


def _reviewer_selected_musicbrainz_ids(record: LibraryRecord, source: SourceRecord) -> ExplicitMusicBrainzIds:
    return ExplicitMusicBrainzIds(
        record.musicbrainz_release_id
        if _has_reviewer_decision(source, 'confirmed') and record.musicbrainz_release_id is not None
        else None,
        record.musicbrainz_recording_id
        if (
            _has_reviewer_decision(source, 'acoustid_confirmed')
            or _has_reviewer_decision(source, 'recording_confirmed')
        )
        and record.musicbrainz_recording_id is not None
        else None,
    )


def _persisted_duration_seconds(source: SourceRecord) -> int | None:
    """The canonical published duration: the persisted source duration, else the newest fingerprint's.

    The persisted fingerprint duration is rounded to whole seconds because it is the same whole-second value
    every provider lookup is asked about.
    """
    duration_seconds = source.duration_seconds
    if duration_seconds is None and source.fingerprints:
        fingerprint_duration = source.fingerprints[-1].duration_seconds
        return None if fingerprint_duration is None else round(fingerprint_duration)
    return duration_seconds


def _matching_request(
    record: LibraryRecord,
    source: SourceRecord,
    tags: tuple[tuple[str, str], ...],
) -> MatchingRequest:
    values = {name: value for name, value in tags}
    duration_seconds = _persisted_duration_seconds(source)
    return MatchingRequest(
        values.get('ARTIST', ''),
        values.get('ALBUM', ''),
        duration_seconds,
        _reviewer_selected_musicbrainz_ids(record, source),
        recording_title=values.get('TITLE', ''),
        track_number=_tag_number(values.get('TRACKNUMBER')),
        track_total=_tag_number(values.get('TRACKTOTAL')),
        disc_number=_tag_number(values.get('DISCNUMBER')),
        disc_total=_tag_number(values.get('DISCTOTAL')),
        source_path=source.source_path,
        album_artist_name=values.get('ALBUMARTIST', ''),
    )


def _stored_release_scores(source: SourceRecord) -> tuple[CandidateScore, ...]:
    latest_run = _latest_candidate_run(source, 'musicbrainz')
    candidates = source.candidates if latest_run is None else latest_run.candidates
    scores: dict[str, float] = {}
    for candidate in reversed(candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'musicbrainz' and evidence.entity == 'recording_release' and evidence.score is not None:
            release_mbid = evidence.release_mbid
            if release_mbid is not None:
                scores[release_mbid] = max(scores.get(release_mbid, 0.0), evidence.score)
    return tuple(CandidateScore(candidate_key, score) for candidate_key, score in scores.items())


def _stored_release_candidate(source: SourceRecord, release_mbid: str) -> tuple[str, CandidateEvidencePayload] | None:
    latest_run = _latest_candidate_run(source, 'musicbrainz')
    candidates = source.candidates if latest_run is None else latest_run.candidates
    matching: list[tuple[str, CandidateEvidencePayload]] = []
    for candidate in candidates:
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if (
            evidence.provider == 'musicbrainz'
            and evidence.entity == 'recording_release'
            and evidence.release_mbid == release_mbid
        ):
            matching.append((candidate.candidate_key, evidence))
    return max(matching, key=lambda item: (item[1].score or -1.0, item[0])) if matching else None


def _tag_number(value: str | None) -> int | None:
    normalized = (value or '').split('/', 1)[0].strip()
    return int(normalized) if normalized.isdecimal() else None


def _folder_selection_root(source_path: str) -> Path:
    return Path(source_path).parent


def _final_tags(
    source_tags: dict[str, str],
    analyzed_tags: dict[str, str],
    match_result: MatchResult | None,
) -> dict[str, str]:
    final = dict(source_tags)
    verified_analysis = match_result is not None and match_result.decision is MatchDecision.AUTO_SELECTED
    for name, value in analyzed_tags.items():
        if verified_analysis or name.startswith('MUSICBRAINZ_'):
            final[name] = value
    return final


def _has_explicit_musicbrainz_identity(tags: dict[str, str]) -> bool:
    return all(tags.get(name, '').strip() for name in ('MUSICBRAINZ_RECORDINGID', 'MUSICBRAINZ_ALBUMID'))
