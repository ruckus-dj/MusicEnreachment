from __future__ import annotations

from music_ingest.contracts import (
    CandidateEvidencePayload,
)
from music_ingest.models.library import CandidateView, SourceRecordView


def _candidate_is_displayable(evidence: CandidateEvidencePayload) -> bool:
    if evidence.provider == 'musicbrainz' and evidence.entity == 'recording_release':
        return evidence.release_mbid is not None and evidence.recording_mbid is not None
    if evidence.provider != 'musicbrainz':
        return True
    return bool(
        evidence.artist.strip() and evidence.release.strip() and evidence.tags.get('MUSICBRAINZ_ALBUMID', '').strip()
    )


def _current_candidates(source: SourceRecordView) -> tuple[CandidateView, ...]:
    latest_run_ids: dict[str, int] = {}
    for run in source.candidate_runs:
        latest_run_ids[run.provider_name] = max(latest_run_ids.get(run.provider_name, 0), run.id)
    return tuple(
        candidate
        for candidate in source.candidates
        if candidate.run_id is None
        or latest_run_ids.get(CandidateEvidencePayload.model_validate_json(candidate.evidence).provider)
        == candidate.run_id
    )


def _merge_candidate_evidence(
    existing: CandidateEvidencePayload,
    incoming: CandidateEvidencePayload,
) -> CandidateEvidencePayload:
    acoustid_score = existing.acoustid_score
    musicbrainz_score = existing.musicbrainz_score
    if existing.provider == 'acoustid' and acoustid_score is None:
        acoustid_score = existing.score
    if existing.provider == 'musicbrainz' and existing.score_components is None and musicbrainz_score is None:
        musicbrainz_score = existing.score
    if incoming.provider == 'acoustid' and incoming.score is not None:
        acoustid_score = incoming.score
    if incoming.provider == 'musicbrainz' and incoming.score_components is None and incoming.score is not None:
        musicbrainz_score = incoming.score
    score_components = incoming.score_components or existing.score_components
    composite_score = (
        incoming.score
        if incoming.score_components is not None
        else existing.score
        if existing.score_components is not None
        else None
    )
    provider = 'musicbrainz' if musicbrainz_score is not None or score_components is not None else 'acoustid'
    compatible_ids = tuple(dict.fromkeys((*existing.compatible_ids, *incoming.compatible_ids)))
    tags = {**existing.tags, **incoming.tags}
    return CandidateEvidencePayload(
        provider=provider,
        entity=incoming.entity,
        artist=incoming.artist or existing.artist,
        release=incoming.release or existing.release,
        title=incoming.title or existing.title,
        album=incoming.album or existing.album,
        release_mbid=incoming.release_mbid or existing.release_mbid,
        recording_mbid=incoming.recording_mbid or existing.recording_mbid,
        compatible_ids=compatible_ids,
        score=(
            composite_score
            if composite_score is not None
            else acoustid_score
            if acoustid_score is not None
            else musicbrainz_score
        ),
        acoustid_score=acoustid_score,
        musicbrainz_score=musicbrainz_score,
        score_components=score_components,
        tags=tags,
        releases=tuple((*existing.releases, *[item for item in incoming.releases if item not in existing.releases])),
    )


def _display_candidates(source: SourceRecordView) -> tuple[tuple[str, CandidateEvidencePayload], ...]:
    merged: dict[tuple[str, str], tuple[str, CandidateEvidencePayload]] = {}
    for candidate in _current_candidates(source):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if not _candidate_is_displayable(evidence):
            continue
        key = (evidence.entity, candidate.candidate_key)
        current = merged.get(key)
        merged[key] = (
            candidate.candidate_key,
            evidence if current is None else _merge_candidate_evidence(current[1], evidence),
        )
    return tuple(
        sorted(
            merged.values(),
            key=lambda item: (
                item[1].score_components is None,
                -(item[1].score if item[1].score_components is not None and item[1].score is not None else 0.0),
                item[0],
            ),
        )
    )
