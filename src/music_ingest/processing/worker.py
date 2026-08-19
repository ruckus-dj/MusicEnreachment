from __future__ import annotations

import json
import logging
import shutil
from collections.abc import Callable
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import Final, final, override
from uuid import uuid4

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from music_ingest.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.dto import ALLOWED_TAG_KEYS, CandidateEvidencePayload, FieldPolicy, GenrePolicy
from music_ingest.enrichment.artwork import (
    ArtworkProvider,
    ArtworkWriteError,
    ManagedArtworkWriteRequest,
    write_managed_release_artwork,
)
from music_ingest.enrichment.fingerprints import (
    FingerprintRequest,
    FingerprintResult,
    FingerprintState,
    fingerprint_source,
    persist_fingerprint,
)
from music_ingest.external.acoustid import AcoustIdV2Adapter
from music_ingest.external.musicbrainz import MusicBrainzV2Adapter
from music_ingest.external.musicbrainz_genres import display_genre_name
from music_ingest.inspectors._tool import ToolEvidence
from music_ingest.inspectors.decoder import DecoderValidationError, validate_decoder
from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.intake.service import IntakeRequest, Origin, SourceId, intake_source
from music_ingest.library.service import (
    append_metadata_revision,
    attach_source,
    ensure_source_record,
    library_record_detail,
    record_event,
    record_publication,
    reevaluate_effective_source_decision,
)
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceResult, ProviderEvidenceService
from music_ingest.matching.providers import (
    AcoustIdMatch,
    AcoustIdProvider,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    FixtureCase,
    FixtureProvenance,
    LiveProvenance,
    LiveTransport,
    Malformed,
    MusicBrainzMatch,
    MusicBrainzProvider,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    RecordingCandidate,
    ReleaseCandidate,
    Timeout,
    Unavailable,
    release_display_title,
)
from music_ingest.matching.scoring import (
    DEFAULT_CONFIDENCE_THRESHOLD,
    CandidateScore,
    ExplicitMusicBrainzIds,
    MatchDecision,
    MatchingRequest,
    MatchResult,
    recording_candidate_matches,
    score_recording_candidate,
    score_release_candidate,
    select_folder_release,
)
from music_ingest.models import (
    ArtworkRecord,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    ProviderCandidateRunRecord,
    ReleaseArtworkRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceTagRecord,
    StorageConfigRecord,
)
from music_ingest.models.entities import DecoderEvidenceRecord
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.models.repositories import DecoderEvidenceRepository, FingerprintRepository
from music_ingest.normalize.metadata import (
    CanonicalSource,
    MetadataWriteError,
)
from music_ingest.processing.media_stage import (
    MediaPipelineInfrastructureError,
    MediaPipelineRequest,
    MediaStagePlan,
    PipelineOutputFailure,
    SourceAudioCorruptionError,
    inspect_source_capability,  # noqa: F401 - retained as a compatibility test seam
    plan_media_stage,
    process_media,
)
from music_ingest.processing.metadata import (
    SourceMetadataError,
    allocate_unsorted_filename,
    fallback_metadata,
    file_hash,
    publication_layout,
    read_tags,
)
from music_ingest.processing.remux import RemuxFailure
from music_ingest.publication import (
    PublicationAttemptRequest,
    acquire_publication_destination_lock,
    cleanup_attempt,
    expose_attempt,
    finalize_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)
from music_ingest.publication.service import (
    PublicationError,
    PublicationRequest,
    publish_release,  # noqa: F401 - retained as a test seam for legacy publication failure cases
    replace_published_audio,
)
from music_ingest.reconciliation import apply_reconciliation_plan, load_reconciliation_snapshot, plan_reconciliation
from music_ingest.sanitizers.flac import FlacSanitizationFailure
from music_ingest.settings import load_runtime_settings
from music_ingest.source_boundary import SourceBoundaryError, resolve_owned_source

LOGGER = logging.getLogger(__name__)
_TAGS_ADAPTER = TypeAdapter(dict[str, str])
_INITIAL_JOB_KINDS: Final = frozenset(
    {'filesystem_scan', 'lidarr_download', 'lidarr_releaseimport', 'lidarr_rename', 'lidarr_albumdelete'}
)


@dataclass(frozen=True, slots=True)
class ProcessingInfrastructureError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


@dataclass(frozen=True, slots=True)
class ProcessingConfig:
    incoming_root: Path
    staging_root: Path
    media_root: Path
    ffmpeg_command: str = 'ffmpeg'
    fpcalc_command: str = 'fpcalc'
    timeout_seconds: float = 10.0
    retry_delay: timedelta = timedelta(seconds=30)
    max_attempts: int = 3
    field_policy: FieldPolicy | None = None
    genre_policy: GenrePolicy | None = None
    live_transport: LiveTransport | None = None
    musicbrainz_provider: MusicBrainzProvider | None = None
    acoustid_provider: AcoustIdProvider | None = None
    artwork_provider: ArtworkProvider | None = None
    confidence_threshold: float = DEFAULT_CONFIDENCE_THRESHOLD
    unsorted_filename_allocator: Callable[[str], str] | None = None


def _analyzed_tags(
    provider_result: ProviderEvidenceResult | None,
    match_result: MatchResult | None,
) -> dict[str, str]:
    analyzed: dict[str, str] = {}
    if provider_result is None:
        return analyzed
    match provider_result.musicbrainz:
        case MusicBrainzMatch(candidate=candidate):
            analyzed.update(_candidate_tags(candidate))
        case _:
            pass
    match provider_result.acoustid:
        case AcoustIdMatch(evidence=evidence) if 'MUSICBRAINZ_RECORDINGID' not in analyzed:
            analyzed['MUSICBRAINZ_RECORDINGID'] = evidence.recording_mbid
        case _:
            pass
    if match_result is not None and match_result.selected_release_mbid is not None:
        analyzed['MUSICBRAINZ_ALBUMID'] = match_result.selected_release_mbid
    return analyzed


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


def _candidate_records(
    source_id: str,
    candidate: ReleaseCandidate,
    release_score: CandidateScore | None,
    request: MatchingRequest | None,
) -> tuple[CandidateRecord, ...]:
    effective_release_score = score_release_candidate(request, candidate) if request is not None else release_score
    release_score_value = None if effective_release_score is None else effective_release_score.score
    release_record = CandidateRecord(
        source_id=source_id,
        candidate_key=candidate.release_mbid,
        evidence=json.dumps(
            {
                'provider': 'musicbrainz',
                'entity': 'release',
                'artist': candidate.artist_name,
                'release': candidate.release_title,
                'disambiguation': candidate.disambiguation,
                'score': release_score_value,
                'compatible_ids': candidate.recording_mbids,
                'score_components': (
                    None
                    if effective_release_score is None
                    else {
                        'artist': effective_release_score.artist_component,
                        'release': effective_release_score.release_component,
                        'duration': effective_release_score.duration_component,
                        'title': effective_release_score.title_component,
                        'track': effective_release_score.track_component,
                    }
                ),
                'tags': _candidate_tags(candidate),
            },
            sort_keys=True,
        ),
    )
    recording_score = None if request is None else score_recording_candidate(request, candidate)
    recording_records = tuple(
        CandidateRecord(
            source_id=source_id,
            candidate_key=recording_mbid,
            evidence=json.dumps(
                {
                    'provider': 'musicbrainz',
                    'entity': 'recording',
                    'artist': '; '.join(candidate.recording_artist_names) or candidate.artist_name,
                    'release': candidate.release_title,
                    'title': candidate.recording_title or '',
                    'album': candidate.release_title,
                    'score': None if recording_score is None else recording_score.score,
                    'score_components': (
                        None
                        if recording_score is None
                        else {
                            'artist': recording_score.artist_component,
                            'title': recording_score.title_component,
                            'duration': recording_score.duration_component,
                            'track': recording_score.track_component,
                        }
                    ),
                    'recording_mbid': recording_mbid,
                    'compatible_ids': (candidate.release_mbid,),
                    'tags': _candidate_tags(candidate),
                },
                sort_keys=True,
            ),
        )
        for recording_mbid in candidate.recording_mbids
    )
    return (*((release_record,) if candidate.recording_mbids else ()), *recording_records)


def _acoustid_recording_mbids(source: SourceRecord) -> tuple[str, ...]:
    recording_mbids: list[str] = []
    for candidate in reversed(source.candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'acoustid':
            recording_mbid = evidence.tags.get('MUSICBRAINZ_RECORDINGID') or evidence.tags.get('MUSICBRAINZ_TRACKID')
            if recording_mbid is not None and recording_mbid not in recording_mbids:
                recording_mbids.append(recording_mbid)
    return tuple(recording_mbids)


def _acoustid_recording_score(source: SourceRecord, recording_mbid: str) -> float:
    for candidate in reversed(source.candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if (
            evidence.provider == 'acoustid'
            and (evidence.tags.get('MUSICBRAINZ_RECORDINGID') or evidence.tags.get('MUSICBRAINZ_TRACKID'))
            == recording_mbid
        ):
            return evidence.score or 0.0
    return 0.0


def _single_scored_candidate(
    source: SourceRecord, provider: str, confidence_threshold: float
) -> tuple[str, CandidateEvidencePayload] | None:
    candidates_by_key: dict[str, CandidateEvidencePayload] = {}
    latest_run = next((run for run in reversed(source.candidate_runs) if run.provider_name == provider), None)
    candidates = source.candidates if latest_run is None else latest_run.candidates
    for candidate in reversed(candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if (
            evidence.provider == provider
            and not (provider == 'musicbrainz' and evidence.entity != 'release')
            and candidate.candidate_key not in candidates_by_key
        ):
            candidates_by_key[candidate.candidate_key] = evidence
    qualified = tuple(
        (candidate_key, evidence)
        for candidate_key, evidence in candidates_by_key.items()
        if evidence.score is not None and evidence.score >= confidence_threshold
    )
    if not qualified:
        return None
    best_score = max(evidence.score or 0.0 for _, evidence in qualified)
    leaders = tuple(candidate for candidate in qualified if (candidate[1].score or 0.0) == best_score)
    return leaders[0] if len(leaders) == 1 else None


def _single_scored_recording_candidate(
    source: SourceRecord, confidence_threshold: float, preferred_recording_mbid: str | None = None
) -> tuple[str, CandidateEvidencePayload] | None:
    candidates_by_recording: dict[str, tuple[float, CandidateEvidencePayload]] = {}
    for provider in ('acoustid', 'musicbrainz'):
        latest_run = next((run for run in reversed(source.candidate_runs) if run.provider_name == provider), None)
        candidates = source.candidates if latest_run is None else latest_run.candidates
        for candidate in reversed(candidates):
            evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
            if evidence.provider != provider or evidence.entity != 'recording' or evidence.score is None:
                continue
            recording_mbid = (
                evidence.recording_mbid
                or evidence.tags.get('MUSICBRAINZ_RECORDINGID')
                or evidence.tags.get('MUSICBRAINZ_TRACKID')
                or candidate.candidate_key
            )
            current = candidates_by_recording.get(recording_mbid)
            if current is None or current[0] < evidence.score:
                candidates_by_recording[recording_mbid] = (evidence.score, evidence)
    if preferred_recording_mbid is not None:
        preferred = candidates_by_recording.get(preferred_recording_mbid)
        if preferred is not None and preferred[0] >= confidence_threshold:
            return preferred_recording_mbid, preferred[1]
    qualified = tuple(
        (recording_mbid, candidate[1])
        for recording_mbid, candidate in candidates_by_recording.items()
        if candidate[0] >= confidence_threshold
    )
    return qualified[0] if len(qualified) == 1 else None


def _stored_match_tags(
    recording: tuple[str, CandidateEvidencePayload] | None,
    release: tuple[str, CandidateEvidencePayload] | None,
) -> dict[str, str]:
    if release is None:
        return {}
    if recording is None:
        release_mbid, release_evidence = release
        return {**release_evidence.tags, 'MUSICBRAINZ_ALBUMID': release_mbid}
    recording_mbid, _ = recording
    release_mbid, release_evidence = release
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


def _aggregate_musicbrainz_results(results: tuple[ProviderEvidenceResult, ...]) -> MusicBrainzResult:
    candidates: dict[str, ReleaseCandidate] = {}
    provenance: LiveProvenance | FixtureProvenance | None = None
    for result in results:
        match result.musicbrainz:
            case MusicBrainzMatch(provenance=result_provenance, candidate=candidate):
                provenance = result_provenance
                _merge_release_candidate(candidates, candidate)
            case Ambiguous(provenance=result_provenance, candidates=result_candidates):
                provenance = result_provenance
                for candidate in result_candidates:
                    _merge_release_candidate(candidates, candidate)
            case _:
                continue
    if provenance is None or not candidates:
        return results[0].musicbrainz
    unique_candidates = tuple(candidates.values())
    return (
        MusicBrainzMatch(provenance, unique_candidates[0])
        if len(unique_candidates) == 1
        else Ambiguous(provenance, unique_candidates)
    )


def _merge_release_candidate(candidates: dict[str, ReleaseCandidate], candidate: ReleaseCandidate) -> None:
    existing = candidates.get(candidate.release_mbid)
    candidates[candidate.release_mbid] = (
        candidate
        if existing is None
        else replace(
            existing,
            recording_mbids=tuple(dict.fromkeys((*existing.recording_mbids, *candidate.recording_mbids))),
        )
    )


def _unique_acoustid_album_match(
    candidate_matches: tuple[tuple[ProviderEvidenceResult, MatchResult], ...],
) -> tuple[ProviderEvidenceResult, MatchResult] | None:
    automatic_matches = tuple(
        candidate_match
        for candidate_match in candidate_matches
        if candidate_match[1].decision is MatchDecision.AUTO_SELECTED
    )
    if not automatic_matches:
        return None
    best_score = max(match[1].release_score.score for match in automatic_matches)
    best_matches = tuple(match for match in automatic_matches if match[1].release_score.score == best_score)
    return best_matches[0] if len(best_matches) == 1 else None


def _unique_acoustid_recording_match(
    candidate_matches: tuple[tuple[ProviderEvidenceResult, CandidateScore], ...],
    confidence_threshold: float,
) -> tuple[ProviderEvidenceResult, CandidateScore] | None:
    qualified_matches = tuple(
        candidate_match for candidate_match in candidate_matches if candidate_match[1].score >= confidence_threshold
    )
    return qualified_matches[0] if len(qualified_matches) == 1 else None


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


def _matching_request(
    record: LibraryRecord,
    source: SourceRecord,
    tags: tuple[tuple[str, str], ...],
) -> MatchingRequest:
    values = {name: value for name, value in tags}
    duration_seconds = source.duration_seconds
    if duration_seconds is None and source.fingerprints:
        fingerprint_duration = source.fingerprints[-1].duration_seconds
        duration_seconds = None if fingerprint_duration is None else round(fingerprint_duration)
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
    latest_run = next((run for run in reversed(source.candidate_runs) if run.provider_name == 'musicbrainz'), None)
    candidates = source.candidates if latest_run is None else latest_run.candidates
    scores: dict[str, float] = {}
    for candidate in reversed(candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'musicbrainz' and evidence.entity == 'release' and evidence.score is not None:
            _ = scores.setdefault(candidate.candidate_key, evidence.score)
    return tuple(CandidateScore(candidate_key, score) for candidate_key, score in scores.items())


def _stored_release_candidate(source: SourceRecord, release_mbid: str) -> tuple[str, CandidateEvidencePayload] | None:
    latest_run = next((run for run in reversed(source.candidate_runs) if run.provider_name == 'musicbrainz'), None)
    candidates = source.candidates if latest_run is None else latest_run.candidates
    for candidate in reversed(candidates):
        if candidate.candidate_key != release_mbid:
            continue
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'musicbrainz' and evidence.entity == 'release':
            return candidate.candidate_key, evidence
    return None


def _tag_number(value: str | None) -> int | None:
    normalized = (value or '').split('/', 1)[0].strip()
    return int(normalized) if normalized.isdecimal() else None


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


def _apply_match_identity(record: LibraryRecord, match_result: MatchResult | None) -> None:
    if match_result is None or match_result.decision is not MatchDecision.AUTO_SELECTED:
        return
    record.match_state = 'matched'
    record.musicbrainz_release_id = match_result.selected_release_mbid
    record.musicbrainz_recording_id = match_result.recording_score.candidate_mbid


def _apply_match_identity_safely(
    session: Session,
    record: LibraryRecord,
    match_result: MatchResult | None,
    source_id: str,
    now: datetime,
) -> bool:
    if match_result is None or match_result.decision is not MatchDecision.AUTO_SELECTED:
        return True
    try:
        with session.begin_nested():
            _apply_match_identity(record, match_result)
            session.flush()
    except IntegrityError:
        record.match_state = 'needs_review'
        record_event(
            session,
            record.id,
            'recording_identity_conflict',
            'needs_review',
            'verified recording identity is already assigned to another library record',
            now,
            source_id,
        )
        return False
    return True


def _apply_independent_match_identity(
    record: LibraryRecord,
    recording_mbid: str | None,
    release_mbid: str | None,
) -> None:
    if recording_mbid is not None:
        record.musicbrainz_recording_id = recording_mbid
    if release_mbid is not None:
        record.musicbrainz_release_id = release_mbid
    record.match_state = (
        'matched'
        if record.musicbrainz_recording_id is not None and record.musicbrainz_release_id is not None
        else 'needs_review'
    )


def _apply_independent_match_identity_safely(
    session: Session,
    record: LibraryRecord,
    recording_mbid: str | None,
    release_mbid: str | None,
    source_id: str,
    now: datetime,
) -> bool:
    try:
        with session.begin_nested():
            _apply_independent_match_identity(record, recording_mbid, release_mbid)
            session.flush()
    except IntegrityError:
        record.match_state = 'needs_review'
        record_event(
            session,
            record.id,
            'recording_identity_conflict',
            'needs_review',
            'verified recording identity is already assigned to another library record',
            now,
            source_id,
        )
        return False
    return True


@final
class ProcessingWorker:
    def __init__(self, session: Session, config: ProcessingConfig, *, lease_age: timedelta | None = None) -> None:
        self._session: Session = session
        self._config: ProcessingConfig = config
        self._lease_age: timedelta = lease_age or timedelta(minutes=5)

    def run_once(self, *, on_claimed: Callable[[str, str], None] | None = None) -> bool:
        storage = self._session.get(StorageConfigRecord, 1)
        if storage is not None:
            self._config = replace(self._config, media_root=Path(storage.output_root))
        now = datetime.now(UTC)
        reconcile_attempts(self._session, now)
        claimed = JobRepository(self._session).claim_next(now, self._lease_age)
        if claimed is None:
            return False
        if on_claimed is not None:
            on_claimed(claimed.job.id, claimed.job.kind)
        if (
            claimed.reclaimed_stale
            and claimed.job.kind in {'acoustid_analysis', 'musicbrainz_analysis'}
            and claimed.attempt.attempt_number > self._max_attempts()
        ):
            self._retry_claim(claimed, 'provider job exceeded max attempts after stale worker lease', now)
            return True
        try:
            with self._session.begin_nested():
                self._process(claimed, now)
        except MetadataWriteError as error:
            self._retry_claim(claimed, str(error), now, error)
        except ProcessingInfrastructureError as error:
            self._retry_claim(claimed, str(error), now, error)
        except SourceAudioCorruptionError as error:
            source = self._source(claimed)
            self._record_decoder_evidence(source, error.source_evidence, now)
            self._invalid_audio(claimed, source, str(error), now, error)
        except (PipelineOutputFailure, MediaPipelineInfrastructureError, RemuxFailure) as error:
            self._retry_claim(claimed, str(error), now, error)
        except DecoderValidationError as error:
            source = self._source(claimed)
            if error.evidence is not None:
                self._record_decoder_evidence(source, error.evidence, now)
            self._quarantine(claimed, source, str(error), now, error)
        except SourceMetadataError as error:
            source = self._source(claimed)
            self._quarantine(claimed, source, str(error), now, error)
        except (
            OSError,
            PublicationError,
            FlacSanitizationFailure,
            CalledProcessError,
            TimeoutExpired,
        ) as error:
            self._retry_claim(claimed, str(error), now, error)
        except ValueError as error:
            self._retry_claim(claimed, str(error), now, error)
        except Exception as error:  # noqa: BLE001
            self._retry_claim(claimed, f'unexpected processing error: {error}', now, error)
        finally:
            self._discard_staging(claimed.job.id)
            if claimed.attempt.state == 'running':
                JobRepository(self._session).succeed(claimed, datetime.now(UTC))
        return True

    def _process(self, claimed: ClaimedJob, now: datetime) -> None:
        if claimed.job.kind == 'reconciliation_scan':
            snapshot = load_reconciliation_snapshot(self._session, now)
            plan = plan_reconciliation(snapshot)
            claimed.job.result_json = apply_reconciliation_plan(self._session, plan, now).model_dump_json()
            return
        if claimed.job.kind == 'selection_refresh':
            self._process_selection_refresh(claimed, now)
            return
        if claimed.job.kind == 'candidate_selection':
            self._process_candidate_selection(claimed, now)
            return
        if claimed.job.kind in {'acoustid_analysis', 'musicbrainz_analysis'}:
            self._process_analysis(claimed, now)
            return
        if claimed.job.kind == 'folder_release_selection':
            self._process_folder_release_selection(claimed, now)
            return
        if claimed.job.kind == 'final_publish':
            self._process_final_publish(claimed, now)
            return
        if claimed.job.kind == 'artwork_enrichment':
            self._process_artwork_enrichment(claimed, now)
            return
        if claimed.job.kind in _INITIAL_JOB_KINDS:
            self._process_initial(claimed, now)
            return
        raise ProcessingInfrastructureError(f'unsupported processing job kind: {claimed.job.kind}')

    def _process_selection_refresh(self, claimed: ClaimedJob, now: datetime) -> None:
        record_id = claimed.job.library_record_id
        if record_id is None:
            raise ProcessingInfrastructureError('selection refresh requires a library record target')
        record = self._session.scalar(select(LibraryRecord).where(LibraryRecord.id == record_id).with_for_update())
        if record is None:
            raise ProcessingInfrastructureError('selection refresh library record is missing')
        _ = self._session.scalar(
            select(EffectiveSourceDecisionRecord)
            .where(EffectiveSourceDecisionRecord.library_record_id == record.id)
            .with_for_update()
        )
        _ = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.library_record_id == record.id)
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        decision = reevaluate_effective_source_decision(self._session, record_id, now)
        if decision.source_id is None:
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_eligible_source',
                'complete',
                'no eligible source; current managed output was retained',
                now,
            )
            return
        record = library_record_detail(self._session, record_id)
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == decision.source_id and item.layer == 'final'
            ),
            None,
        )
        if revision is None:
            historical_final = self._session.scalar(
                select(LibraryMetadataRevisionRecord)
                .where(LibraryMetadataRevisionRecord.source_id == decision.source_id)
                .where(LibraryMetadataRevisionRecord.layer == 'final')
                .order_by(LibraryMetadataRevisionRecord.created_at.desc(), LibraryMetadataRevisionRecord.id.desc())
            )
            if historical_final is not None:
                revision = append_metadata_revision(
                    self._session,
                    record.id,
                    decision.source_id,
                    'final',
                    _TAGS_ADAPTER.validate_json(historical_final.tags_json),
                    'reassociation_recovery',
                    now,
                )
        if revision is None:
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_final_revision',
                'needs_review',
                'selected source has no final metadata revision; current managed output was retained',
                now,
                decision.source_id,
            )
            return
        publication = next((item for item in record.publications if item.state == 'current'), None)
        if publication is not None and (
            publication.source_id == decision.source_id
            and publication.metadata_revision_id == revision.id
            and publication.path.casefold().endswith('.mka')
        ):
            record_event(
                self._session,
                record_id,
                'selection_refresh_no_change',
                'complete',
                'selected source and final metadata revision already match the current publication',
                now,
                decision.source_id,
            )
            return
        refresh_publish_job = JobRecord(
            id=f'selection-refresh-publish-{claimed.job.id}',
            source_id=decision.source_id,
            kind='final_publish',
            metadata_revision_id=revision.id,
            state='running',
            created_at=now,
        )
        self._process_final_publish(ClaimedJob(refresh_publish_job, claimed.attempt), now)
        record_event(
            self._session,
            record_id,
            'selection_refresh_selected',
            'complete',
            f'effective source {decision.source_id} selected for refresh',
            now,
            decision.source_id,
        )

    def _process_initial(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = self._owned_source_path(claimed, source, now)
        if source_path is None:
            return
        if self._changed(source, source_path):
            self._requeue_changed_source(claimed, source, source_path, now)
            return
        inspection = inspect_media_capability(source_path, timeout_seconds=self._timeout_seconds())
        capability = inspection.capability
        if capability is None:
            detail = inspection.ffprobe.stderr.strip() or inspection.ffprobe.stdout.strip() or 'no ffprobe output'
            self._quarantine(
                claimed,
                source,
                (
                    f'input audio stream policy rejected source: ffprobe={inspection.ffprobe.state}; '
                    f'audio_streams={inspection.audio_stream_count}; {detail}'
                ),
                now,
            )
            return
        decoder_evidence = validate_decoder(
            source_path,
            ffmpeg_command=self._config.ffmpeg_command,
            timeout_seconds=self._timeout_seconds(),
        )
        if decoder_evidence is not None:
            self._record_decoder_evidence(source, decoder_evidence, now)
        source.media_codec = capability.codec.upper()
        technical = inspection.technical
        source.media_bit_depth = None if technical is None else technical.bit_depth
        source.media_sample_rate = None if technical is None else technical.sample_rate
        source.media_channels = None if technical is None else technical.channels
        source.media_bitrate = None if technical is None else technical.bitrate
        cached_fingerprint = self._cached_fingerprint(source)
        plan = plan_media_stage(source_path)
        tags = plan.source_tags
        self._capture_observations(source, source_path, tags)
        original_tags = dict(tags)
        metadata = plan.metadata
        record = ensure_source_record(self._session, source, now)
        _ = append_metadata_revision(self._session, record.id, source.id, 'original', original_tags, 'source', now)
        configured_musicbrainz, configured_acoustid, _ = self._configured_providers()
        providers_enabled = configured_musicbrainz is not None or configured_acoustid is not None
        if not _has_explicit_musicbrainz_identity(original_tags):
            if self._analyze_source(source, source_path) is None:
                return
            source.intake_state = 'present'
            _ = reevaluate_effective_source_decision(self._session, record.id, now)
            if providers_enabled:
                provider_job = 'acoustid_analysis' if configured_acoustid is not None else 'musicbrainz_analysis'
                _ = JobRepository(self._session).enqueue(source.id, provider_job, now)
                record_event(
                    self._session,
                    record.id,
                    'publication_deferred_for_identity',
                    'needs_review',
                    'audio remains unpublished until explicit MusicBrainz recording and release identities are '
                    + 'available',
                    now,
                    source.id,
                )
            else:
                record_event(
                    self._session,
                    record.id,
                    'publication_deferred_for_identity',
                    'needs_review',
                    'audio remains unpublished because explicit MusicBrainz recording and release identities are '
                    + 'missing',
                    now,
                    source.id,
                )
            return
        staged_release = self._staging_directory(claimed.job.id)
        relative_directory, output_name = plan.relative_directory, plan.output_name
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        destination_release = (
            Path(current_publication.path).parent
            if current_publication is not None
            else self._config.media_root / relative_directory
        )
        unsorted_destination = relative_directory == 'Unsorted' and current_publication is None
        if unsorted_destination:
            output_name = f'.{claimed.job.id}.mka'
        pipeline_result = process_media(
            MediaPipelineRequest(
                plan,
                capability,
                staged_release,
                output_name,
                self._config.ffmpeg_command,
                self._config.fpcalc_command,
                self._timeout_seconds(),
                load_runtime_settings(self._session) if metadata is not None else None,
                cached_fingerprint is None,
            )
        )
        if pipeline_result.fingerprint is not None and cached_fingerprint is None:
            persist_fingerprint(self._session, SourceId(source.id), pipeline_result.fingerprint)
        source.media_codec = capability.codec.upper()
        written_tags = pipeline_result.written_tags
        self._session.flush()
        if unsorted_destination:
            output_name = self._allocate_unsorted_filename('.mka')
        target_audio = destination_release / output_name
        path_owner = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.path == str(target_audio.resolve()))
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        if path_owner is not None and path_owner.library_record_id != record.id:
            record_event(
                self._session,
                record.id,
                'publication_path_conflict',
                'needs_review',
                'canonical audio path is already owned by another library record',
                now,
                source.id,
            )
            return
        acquire_publication_destination_lock(self._session, target_audio)
        self._session.refresh(source)
        if source.intake_state == 'replaced':
            record_event(
                self._session,
                record.id,
                'source_generation_superseded',
                'superseded',
                'source was replaced while processing; newer generation is queued',
                now,
                source.id,
            )
            return
        request = PublicationRequest(
            staged_release,
            self._config.staging_root,
            self._config.media_root,
            (source_path,),
            require_canonical_tags=False,
            destination_release=destination_release,
            replace_existing=current_publication is not None,
            destination_audio_name=(
                output_name
                if unsorted_destination
                else Path(current_publication.path).name
                if current_publication is not None
                else None
            ),
            sources=(source,),
        )
        result = replace_published_audio(request, target_audio)
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', dict(written_tags), 'worker', now
        )
        audio_path = result.published_audio
        if audio_path is None:
            audio_path = (
                Path(current_publication.path)
                if current_publication is not None
                else next(result.published_release.glob('*.mka'))
            )
        _ = record_publication(
            self._session,
            record.id,
            source.id,
            audio_path,
            file_hash(audio_path),
            final_revision.id,
            now,
        )
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(
            self._session,
            record.id,
            'publication_ready_for_analysis' if providers_enabled else 'publication_ready_for_review',
            'analyzing' if providers_enabled else 'needs_review',
            'initial final metadata published; provider analysis queued'
            if providers_enabled
            else 'initial final metadata published; no providers configured',
            now,
            source.id,
        )
        if providers_enabled:
            provider_job = 'acoustid_analysis' if configured_acoustid is not None else 'musicbrainz_analysis'
            _ = JobRepository(self._session).enqueue(source.id, provider_job, now)
        if not written_tags:
            record_event(
                self._session,
                record.id,
                'metadata_required',
                'analyzing' if providers_enabled else 'needs_review',
                'audio published without usable metadata',
                now,
                source.id,
            )

    def _process_analysis(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = self._owned_source_path(claimed, source, now)
        if source_path is None:
            return
        if self._changed(source, source_path):
            self._requeue_changed_source(claimed, source, source_path, now)
            return
        fingerprint = self._analyze_source(source, source_path)
        if fingerprint is None:
            return
        tags = read_tags(source_path)
        record = ensure_source_record(self._session, source, now)
        recording_mbid, release_mbid = musicbrainz_lookup_ids(record, source)
        provider_result = self._lookup_providers(
            tags,
            fingerprint,
            now,
            force_refresh=True,
            recording_mbid=recording_mbid,
            release_mbid=release_mbid,
            run_acoustid=claimed.job.kind == 'acoustid_analysis',
            run_musicbrainz=claimed.job.kind == 'musicbrainz_analysis',
        )
        if provider_result is None:
            record_event(
                self._session,
                record.id,
                'analysis_ready_for_review',
                'needs_review',
                'provider analysis could not build a query from the available source metadata',
                now,
                source.id,
            )
            return
        if claimed.job.kind == 'acoustid_analysis':
            acoustic_result = provider_result.acoustid
            match acoustic_result:
                case AcoustIdMatch():
                    pass
                case None | Ambiguous() | Disabled() | Malformed() | NoMatch() | RateLimited() | Timeout():
                    pass
                case Unavailable():
                    pass
            if acoustic_result is not None:
                _ = self._capture_provider_attempt(source, 'acoustid', acoustic_result, None, now)
            configured_musicbrainz, _, _ = self._configured_providers()
            if configured_musicbrainz is not None:
                _ = JobRepository(self._session).enqueue(source.id, 'musicbrainz_analysis', now)
                record_event(
                    self._session,
                    record.id,
                    'acoustid_analysis_ready',
                    'analyzing',
                    'AcousticID analysis is complete; MusicBrainz analysis queued',
                    now,
                    source.id,
                )
            else:
                self._enqueue_candidate_selection_if_ready(source, claimed.job.id, now)
                record_event(
                    self._session,
                    record.id,
                    'acoustid_analysis_ready',
                    'needs_review',
                    'AcousticID analysis is complete; MusicBrainz is not configured',
                    now,
                    source.id,
                )
            return
        candidate_request = _matching_request(record, source, tags)
        candidate_results = [provider_result]
        if claimed.job.kind == 'musicbrainz_analysis':
            for candidate_recording_mbid in _acoustid_recording_mbids(source):
                candidate_result = (
                    provider_result
                    if candidate_recording_mbid == recording_mbid
                    else self._lookup_providers(
                        tags,
                        fingerprint,
                        now,
                        force_refresh=True,
                        recording_mbid=candidate_recording_mbid,
                        release_mbid=None,
                        run_acoustid=False,
                        run_musicbrainz=True,
                    )
                )
                if candidate_result is None:
                    continue
                match candidate_result.musicbrainz:
                    case MusicBrainzMatch(candidate=candidate):
                        candidates = (candidate,)
                    case Ambiguous(candidates=candidates):
                        pass
                    case _:
                        continue
                matching_candidates = tuple(
                    candidate
                    for candidate in candidates
                    if candidate_recording_mbid in candidate.recording_mbids
                    and recording_candidate_matches(candidate_request, candidate)
                )
                if len(matching_candidates) == 1:
                    candidate_results.append(
                        replace(
                            candidate_result,
                            musicbrainz=MusicBrainzMatch(
                                candidate_result.musicbrainz.provenance,
                                matching_candidates[0],
                            ),
                        )
                    )
        _ = self._capture_provider_attempt(
            source,
            'musicbrainz',
            _aggregate_musicbrainz_results(tuple(candidate_results)),
            None,
            now,
            candidate_request,
        )
        self._enqueue_candidate_selection_if_ready(source, claimed.job.id, now)

    def _process_final_publish(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        record = ensure_source_record(self._session, source, now)
        decision = reevaluate_effective_source_decision(self._session, record.id, now)
        if decision.source_id is not None and decision.source_id != source.id:
            record_event(
                self._session,
                record.id,
                'final_publish_stale_source',
                'complete',
                'final publication source is no longer the effective source',
                now,
                source.id,
            )
            return
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.layer == 'final'
                and (claimed.job.metadata_revision_id is None or item.id == claimed.job.metadata_revision_id)
            ),
            None,
        )
        if revision is None:
            raise ValueError('final metadata revision is missing')
        final_tags = _TAGS_ADAPTER.validate_json(revision.tags_json)
        source_path = self._owned_source_path(claimed, source, now)
        if source_path is None:
            return
        relative_directory, output_name = publication_layout(tuple(final_tags.items()), source_path.name)
        publication = next((item for item in record.publications if item.state == 'current'), None)
        unsorted_destination = relative_directory == 'Unsorted' and publication is None
        if unsorted_destination:
            output_name = self._allocate_unsorted_filename('.mka')
        target_audio = self._config.media_root / relative_directory / output_name
        if publication is not None and (
            publication.source_id == source.id
            and publication.metadata_revision_id == revision.id
            and Path(publication.path).resolve() == target_audio.resolve()
        ):
            record_event(
                self._session,
                record.id,
                'final_publish_no_change',
                'complete',
                'source, final metadata revision, and output extension already match the current publication',
                now,
                source.id,
            )
            return
        path_owner = self._session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.path == str(target_audio.resolve()))
            .where(LibraryPublicationRecord.state == 'current')
            .with_for_update()
        )
        if path_owner is not None and path_owner.library_record_id != record.id:
            record_event(
                self._session,
                record.id,
                'publication_path_conflict',
                'needs_review',
                'canonical audio path is already owned by another library record',
                now,
                source.id,
            )
            return
        destination_release = target_audio.parent
        attempt_token = uuid4().hex
        attempt = reserve_attempt(
            self._session,
            PublicationAttemptRequest(
                f'publication-attempt-{attempt_token}',
                record.id,
                source.id,
                revision.id,
                destination_release,
                output_name,
                self._config.staging_root / 'publication-attempts' / attempt_token,
                self._config.staging_root / 'publication-backups' / attempt_token,
                now,
            ),
        )
        staged_release = Path(attempt.staging_directory)
        staged_release.parent.mkdir(parents=True, exist_ok=True)
        staged_release.mkdir()
        capability = inspect_source_capability(source_path, timeout_seconds=self._timeout_seconds())
        if capability is None:
            raise ProcessingInfrastructureError('source has no declared media capability')
        metadata = fallback_metadata(tuple(final_tags.items()), CanonicalSource.REVIEWED_MANUAL)
        pipeline_plan = MediaStagePlan(
            source_path, tuple(final_tags.items()), metadata, relative_directory, output_name
        )
        _ = process_media(
            MediaPipelineRequest(
                pipeline_plan,
                capability,
                staged_release,
                output_name,
                self._config.ffmpeg_command,
                self._config.fpcalc_command,
                self._timeout_seconds(),
                load_runtime_settings(self._session) if metadata is not None else None,
                False,
            )
        )
        mark_staged(self._session, attempt, now)
        if not unsorted_destination:
            acquire_publication_destination_lock(self._session, target_audio)
        expose_attempt(self._session, attempt, now)
        _ = finalize_attempt(self._session, attempt, now)
        cleanup_attempt(attempt)
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        release_mbid = final_tags.get('MUSICBRAINZ_ALBUMID', '').strip()
        if release_mbid and self._artwork_enabled():
            _ = JobRepository(self._session).enqueue_release_artwork(release_mbid, now)
        record_event(self._session, record.id, 'final_published', 'complete', None, now, source.id)

    def _process_artwork_enrichment(self, claimed: ClaimedJob, now: datetime) -> None:
        release_mbid = claimed.job.release_mbid
        if release_mbid is None:
            raise ProcessingInfrastructureError('artwork enrichment requires a release MBID target')
        if not self._artwork_enabled():
            return
        artwork = self._session.get(ReleaseArtworkRecord, release_mbid)
        if artwork is not None and artwork.state == 'ready' and artwork.path is not None:
            if Path(artwork.path).is_file():
                return
            artwork.state = 'missing'
            artwork.path = None
            artwork.format_name = None
            artwork.updated_at = now
        if self._config.artwork_provider is None:
            raise ProcessingInfrastructureError('artwork provider is unavailable')
        publication = self._session.scalar(
            select(LibraryPublicationRecord)
            .join(LibraryRecord, LibraryRecord.id == LibraryPublicationRecord.library_record_id)
            .where(LibraryRecord.musicbrainz_release_id == release_mbid)
            .where(LibraryPublicationRecord.state == 'current')
            .order_by(LibraryPublicationRecord.created_at.desc())
        )
        if publication is None:
            raise ProcessingInfrastructureError('published album for artwork release is missing')
        release_directory = Path(publication.path).resolve().parent
        media_root = self._config.media_root.resolve(strict=True)
        if release_directory == media_root or media_root not in release_directory.parents:
            raise ProcessingInfrastructureError('published album is outside the managed media root')
        existing_cover = next(
            (path for path in (release_directory / 'cover.jpg', release_directory / 'cover.webp') if path.is_file()),
            None,
        )
        if existing_cover is not None:
            self._save_artwork_state(artwork, release_mbid, existing_cover, now)
            return
        candidate = self._config.artwork_provider.fetch_artwork(release_mbid)
        if candidate is None:
            raise ProcessingInfrastructureError('artwork provider returned no cover')
        try:
            output = write_managed_release_artwork(
                ManagedArtworkWriteRequest(self._config.media_root, release_directory, release_mbid, candidate)
            )
        except ArtworkWriteError as error:
            if str(error) != 'release already has artwork':
                raise
            output = next(
                (
                    path
                    for path in (release_directory / 'cover.jpg', release_directory / 'cover.webp')
                    if path.is_file()
                ),
                None,
            )
            if output is None:
                raise
        self._save_artwork_state(artwork, release_mbid, output, now)

    def _save_artwork_state(
        self, artwork: ReleaseArtworkRecord | None, release_mbid: str, output: Path, now: datetime
    ) -> None:
        if artwork is None:
            artwork = ReleaseArtworkRecord(
                release_mbid=release_mbid,
                path=str(output),
                format_name=output.suffix.removeprefix('.'),
                provider='musicbrainz',
                state='ready',
                created_at=now,
                updated_at=now,
            )
            self._session.add(artwork)
        else:
            artwork.path = str(output)
            artwork.format_name = output.suffix.removeprefix('.')
            artwork.provider = 'musicbrainz'
            artwork.state = 'ready'
            artwork.updated_at = now
        self._session.flush()

    def _artwork_enabled(self) -> bool:
        return load_runtime_settings(self._session).artwork_enabled

    def _lookup_providers(
        self,
        tags: tuple[tuple[str, str], ...],
        fingerprint: FingerprintResult,
        now: datetime,
        force_refresh: bool = False,
        recording_mbid: str | None = None,
        release_mbid: str | None = None,
        run_acoustid: bool = True,
        run_musicbrainz: bool = True,
    ) -> ProviderEvidenceResult | None:
        values = {name: value for name, value in tags}
        query = (
            f'artist:"{values["ARTIST"]}" release:"{values["ALBUM"]}" '
            + (f'recording:"{values["TITLE"]}"' if values.get('TITLE') else '')
            if {'ARTIST', 'ALBUM'} <= values.keys()
            else ''
        )
        configured_musicbrainz, configured_acoustid, _ = self._configured_providers()
        musicbrainz = configured_musicbrainz if run_musicbrainz and (query or recording_mbid or release_mbid) else None
        acoustid = (
            configured_acoustid
            if run_acoustid and fingerprint.fingerprint is not None and fingerprint.duration_seconds is not None
            else None
        )
        if musicbrainz is None and acoustid is None:
            return None
        return ProviderEvidenceService(self._session, musicbrainz, acoustid, commit_on_persist=False).lookup(
            ProviderEvidenceRequest(
                query,
                FixtureCase.SUCCESS,
                fingerprint.fingerprint,
                FixtureCase.SUCCESS if acoustid is not None else None,
                duration_seconds=fingerprint.duration_seconds,
                force_refresh=force_refresh,
                release_title=values.get('ALBUM'),
                recording_title=values.get('TITLE'),
                track_number=_tag_number(values.get('TRACKNUMBER')),
                acoustid_confidence_threshold=self._confidence_threshold(),
                artist_name=values.get('ARTIST'),
                recording_mbid=recording_mbid,
                release_mbid=release_mbid,
                run_acoustid=run_acoustid,
                run_musicbrainz=run_musicbrainz,
            ),
            now,
        )

    def _enqueue_candidate_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        active_collection = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.source_id == source.id)
            .where(JobRecord.kind.in_(['filesystem_scan', 'acoustid_analysis', 'musicbrainz_analysis']))
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.id != current_job_id)
        )
        if active_collection is not None:
            return
        musicbrainz, acoustid, _ = self._configured_providers()
        required_providers = tuple(
            provider_name
            for provider_name, provider in (('musicbrainz', musicbrainz), ('acoustid', acoustid))
            if provider is not None
        )
        if any(
            not any(run.provider_name == provider_name for run in reversed(source.candidate_runs))
            for provider_name in required_providers
        ):
            return
        _ = JobRepository(self._session).enqueue(source.id, 'candidate_selection', now)

    def _process_candidate_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        record = ensure_source_record(self._session, source, now)
        stored_release = _single_scored_candidate(source, 'musicbrainz', self._confidence_threshold())
        release_recording_mbid = _stored_release_recording_mbid(stored_release)
        stored_recording = (
            _single_scored_recording_candidate(source, self._confidence_threshold(), release_recording_mbid)
            if stored_release is not None and release_recording_mbid is not None
            else None
        )
        pair_is_qualified = (
            stored_release is not None
            and release_recording_mbid is not None
            and stored_recording is not None
            and stored_recording[0] == release_recording_mbid
        )
        if not pair_is_qualified:
            record.processing_state = 'needs_review'
            record.match_state = 'needs_review'
            record_event(
                self._session,
                record.id,
                'analysis_ready_for_review',
                'needs_review',
                'recording and release candidates did not form one confidence-qualified pair',
                now,
                source.id,
            )
            self._enqueue_folder_selection_if_ready(source, claimed.job.id, now)
            return
        if stored_release is None or stored_recording is None:
            self._enqueue_folder_selection_if_ready(source, claimed.job.id, now)
            return
        recording_mbid = stored_recording[0]
        recording_evidence = stored_recording[1]
        associated = RecordingAssociationService(self._session).associate_automatic(
            AutomaticAssociationRequest(
                source.id,
                recording_mbid,
                recording_evidence.score or 0.0,
                self._confidence_threshold(),
                json.dumps({'recording_mbid': recording_mbid, 'release_mbid': stored_release[0]}, sort_keys=True),
                now,
                release_mbid=stored_release[0],
            )
        )
        if associated is None:
            return
        record = library_record_detail(self._session, associated.library_record_id)
        analyzed_tags = _stored_match_tags(stored_recording, stored_release)
        record_event(
            self._session,
            record.id,
            'stored_candidates_auto_selected',
            record.match_state,
            'stored qualifying provider candidates selected after provider collection completed',
            now,
            source.id,
        )
        if analyzed_tags:
            source_tags = {
                item.tag_name: item.value for item in source.tag_observations if item.tag_name in ALLOWED_TAG_KEYS
            }
            _ = append_metadata_revision(
                self._session, record.id, source.id, 'analyzed', analyzed_tags, 'provider_selection', now
            )
            final_revision = append_metadata_revision(
                self._session,
                record.id,
                source.id,
                'final',
                {**source_tags, **analyzed_tags},
                'provider_selection',
                now,
            )
            _ = JobRepository(self._session).enqueue(source.id, 'final_publish', now, final_revision.id)
            record_event(
                self._session,
                record.id,
                'analysis_ready_for_publish',
                'publishing',
                'stored provider metadata is ready for final publication',
                now,
                source.id,
            )
        self._enqueue_folder_selection_if_ready(source, claimed.job.id, now)

    def _enqueue_folder_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        folder = Path(source.source_path).parent
        members = tuple(
            item
            for item in self._session.scalars(
                select(SourceRecord).where(SourceRecord.library_record_id.is_not(None))
            ).all()
            if Path(item.source_path).parent == folder
            and item.disappeared_at is None
            and item.intake_state != 'replaced'
        )
        if len(members) < 2:
            return
        member_ids = tuple(item.id for item in members)
        active_collection = self._session.scalar(
            select(JobRecord)
            .where(JobRecord.source_id.in_(member_ids))
            .where(
                JobRecord.kind.in_(
                    [
                        'filesystem_scan',
                        'acoustid_analysis',
                        'musicbrainz_analysis',
                        'candidate_selection',
                    ]
                )
            )
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.id != current_job_id)
        )
        if active_collection is not None:
            return
        if any(
            not any(run.provider_name == 'musicbrainz' for run in reversed(item.candidate_runs)) for item in members
        ):
            return
        _ = JobRepository(self._session).enqueue_folder_release_selection(str(folder), now)

    def _process_folder_release_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        folder_path = claimed.job.folder_path
        if folder_path is None:
            raise ProcessingInfrastructureError('folder release selection requires a folder target')
        folder = Path(folder_path)
        members = tuple(
            item
            for item in self._session.scalars(
                select(SourceRecord).where(SourceRecord.library_record_id.is_not(None))
            ).all()
            if Path(item.source_path).parent == folder
            and item.disappeared_at is None
            and item.intake_state != 'replaced'
        )
        if not members:
            return
        member_ids = tuple(item.id for item in members)
        if (
            self._session.scalar(
                select(JobRecord)
                .where(JobRecord.source_id.in_(member_ids))
                .where(JobRecord.kind.in_(['filesystem_scan', 'acoustid_analysis', 'musicbrainz_analysis']))
                .where(JobRecord.state.in_(['queued', 'running']))
            )
            is not None
        ):
            return
        groups = tuple(_stored_release_scores(item) for item in members)
        selected_release = select_folder_release(groups)
        selected_scores = (
            tuple(
                next((score.score for score in group if score.candidate_mbid == selected_release), 0.0)
                for group in groups
            )
            if selected_release is not None
            else ()
        )
        if selected_release is None or any(score < self._confidence_threshold() for score in selected_scores):
            for source in members:
                if source.library_record_id is not None:
                    record = library_record_detail(self._session, source.library_record_id)
                    record.processing_state = 'needs_review'
                    record.match_state = 'needs_review'
                    record_event(
                        self._session,
                        source.library_record_id,
                        'folder_release_selection_review',
                        'needs_review',
                        'folder candidates have no unique shared release',
                        now,
                        source.id,
                    )
            return
        for source in members:
            if source.library_record_id is None:
                continue
            release = _stored_release_candidate(source, selected_release)
            if release is None:
                record = library_record_detail(self._session, source.library_record_id)
                record.processing_state = 'needs_review'
                record.match_state = 'needs_review'
                record_event(
                    self._session,
                    source.library_record_id,
                    'folder_release_selection_review',
                    'needs_review',
                    'selected folder release is absent from the source candidate run',
                    now,
                    source.id,
                )
                continue
            record = library_record_detail(self._session, source.library_record_id)
            recording_mbid = record.musicbrainz_recording_id or _stored_release_recording_mbid(release)
            if not _apply_independent_match_identity_safely(
                self._session, record, recording_mbid, selected_release, source.id, now
            ):
                continue
            analyzed_tags = _stored_match_tags(None, release)
            source_tags = {
                item.tag_name: item.value for item in source.tag_observations if item.tag_name in ALLOWED_TAG_KEYS
            }
            _ = append_metadata_revision(
                self._session, record.id, source.id, 'analyzed', analyzed_tags, 'folder_selection', now
            )
            final_revision = append_metadata_revision(
                self._session,
                record.id,
                source.id,
                'final',
                {**source_tags, **analyzed_tags},
                'folder_selection',
                now,
            )
            _ = JobRepository(self._session).enqueue(source.id, 'final_publish', now, final_revision.id)
            record_event(
                self._session,
                record.id,
                'folder_release_selected',
                'publishing',
                f'folder release {selected_release} selected from complete candidate runs',
                now,
                source.id,
            )

    def _confidence_threshold(self) -> float:
        if self._config.live_transport is not None:
            return load_runtime_settings(self._session).confidence_threshold
        setting = self._session.get(RuntimeSettingRecord, 'matching.confidence_threshold')
        if setting is None:
            return self._config.confidence_threshold
        try:
            value = float(setting.value)
        except ValueError:
            return self._config.confidence_threshold
        return value if 0.0 <= value <= 1.0 else self._config.confidence_threshold

    def _capture_provider_attempt(
        self,
        source: SourceRecord,
        provider_name: str,
        result: MusicBrainzResult | AcoustIdResult,
        _match_result: MatchResult | None,
        now: datetime,
        request: MatchingRequest | None = None,
    ) -> str | None:
        match result:
            case (
                MusicBrainzMatch(provenance=provenance)
                | AcoustIdMatch(provenance=provenance)
                | NoMatch(provenance=provenance)
                | Ambiguous(provenance=provenance)
                | Disabled(provenance=provenance)
                | Malformed(provenance=provenance)
                | RateLimited(provenance=provenance)
                | Timeout(provenance=provenance)
                | Unavailable(provenance=provenance)
            ):
                match provenance:
                    case LiveProvenance(request_hash=request_hash, sha256=response_sha256, http_status=http_status):
                        snapshot = json.dumps(
                            {'request_hash': request_hash, 'http_status': http_status, 'sha256': response_sha256},
                            sort_keys=True,
                        )
                    case FixtureProvenance(path=path, sha256=response_sha256):
                        snapshot = json.dumps({'path': str(path), 'sha256': response_sha256}, sort_keys=True)
                source.provider_attempts.append(
                    ProviderAttemptRecord(
                        provider_name=provider_name,
                        outcome=type(result).__name__.casefold(),
                        snapshot_sha256=provenance.sha256,
                        snapshot=snapshot,
                        created_at=now,
                    )
                )
                run = ProviderCandidateRunRecord(
                    source_id=source.id,
                    provider_name=provider_name,
                    created_at=now,
                )
                source.candidate_runs.append(run)
                candidate_scores: dict[str | None, CandidateScore] = {}
                if provider_name == 'musicbrainz' and request is not None:
                    match result:
                        case MusicBrainzMatch(candidate=candidate):
                            score = score_release_candidate(request, candidate)
                            candidate_scores[score.candidate_mbid] = score
                        case Ambiguous(candidates=candidates):
                            for candidate in candidates:
                                score = score_release_candidate(request, candidate)
                                candidate_scores[score.candidate_mbid] = score
                        case _:
                            pass
                match result:
                    case MusicBrainzMatch(candidate=candidate):
                        candidate_records = _candidate_records(
                            source.id, candidate, candidate_scores.get(candidate.release_mbid), request
                        )
                        source.candidates.extend(candidate_records)
                        run.candidates.extend(candidate_records)
                    case Ambiguous(candidates=candidates):
                        candidate_records = tuple(
                            record
                            for candidate in candidates
                            for record in _candidate_records(
                                source.id, candidate, candidate_scores.get(candidate.release_mbid), request
                            )
                        )
                        source.candidates.extend(candidate_records)
                        run.candidates.extend(candidate_records)
                    case AcoustIdMatch(evidence=evidence):
                        recordings = evidence.candidates or (
                            RecordingCandidate(evidence.recording_mbid, evidence.score),
                        )
                        candidate_records = tuple(
                            CandidateRecord(
                                source_id=source.id,
                                candidate_key=recording.recording_mbid,
                                evidence=json.dumps(
                                    {
                                        'provider': 'acoustid',
                                        'entity': 'recording',
                                        'recording_mbid': recording.recording_mbid,
                                        'score': recording.score,
                                        'artist': '',
                                        'release': '',
                                        'title': '',
                                        'album': '',
                                        'compatible_ids': (),
                                        'tags': {'MUSICBRAINZ_RECORDINGID': recording.recording_mbid},
                                    },
                                    sort_keys=True,
                                ),
                            )
                            for recording in recordings
                        )
                        source.candidates.extend(candidate_records)
                        run.candidates.extend(candidate_records)
                        return None
                    case NoMatch() | Disabled() | Malformed() | RateLimited() | Timeout() | Unavailable():
                        return None
        return None

    def _source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self._session.get(SourceRecord, claimed.job.source_id)
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def _owned_source_path(self, claimed: ClaimedJob, source: SourceRecord, now: datetime) -> Path | None:
        try:
            return resolve_owned_source(source)
        except SourceBoundaryError as error:
            self._quarantine(claimed, source, f'root boundary: {error}', now)
            return None

    def _changed(self, source: SourceRecord, path: Path) -> bool:
        if source.mtime_ns == 0:
            return False
        stat = path.stat()
        return (stat.st_dev, stat.st_ino, stat.st_size, stat.st_mtime_ns) != (
            source.device,
            source.inode,
            source.size_bytes,
            source.mtime_ns,
        )

    def _cached_fingerprint(self, source: SourceRecord) -> FingerprintResult | None:
        fingerprint = FingerprintRepository(self._session).successful_evidence(source.id)
        if fingerprint is None:
            return None
        return FingerprintResult(
            FingerprintState(fingerprint.state),
            fingerprint.fingerprint,
            fingerprint.duration_seconds,
            fingerprint.tool_version,
            fingerprint.output_sha256,
            None,
            None,
        )

    def _analyze_source(self, source: SourceRecord, source_path: Path) -> FingerprintResult | None:
        cached_fingerprint = self._cached_fingerprint(source)
        if cached_fingerprint is not None:
            return cached_fingerprint
        return fingerprint_source(
            self._session,
            FingerprintRequest(SourceId(source.id), source_path, None),
            fpcalc_command=self._config.fpcalc_command,
            timeout_seconds=self._timeout_seconds(),
        )

    def _record_decoder_evidence(self, source: SourceRecord, evidence: ToolEvidence, now: datetime) -> None:
        _ = DecoderEvidenceRepository(self._session).add_evidence(
            DecoderEvidenceRecord(
                source_id=source.id,
                decoder_command=self._config.ffmpeg_command,
                tool_state=evidence.state.value,
                return_code=evidence.return_code,
                output_sha256=sha256(evidence.stdout.encode()).hexdigest(),
                checked_at=now,
            )
        )

    def _capture_observations(self, source: SourceRecord, path: Path, tags: tuple[tuple[str, str], ...]) -> None:
        if source.tag_observations:
            return
        source.tag_observations.extend(
            SourceTagRecord(format_name='vorbis', tag_name=name, value=value) for name, value in tags
        )
        for artwork in (path.parent / 'cover.jpg', path.parent / 'cover.webp'):
            if artwork.is_file():
                source.artwork_observations.append(ArtworkRecord(sha256=file_hash(artwork)))

    def _staging_directory(self, job_id: str) -> Path:
        directory = self._config.staging_root / job_id
        self._config.staging_root.mkdir(parents=True, exist_ok=True)
        if directory.exists():
            shutil.rmtree(directory)
        directory.mkdir()
        return directory

    def _discard_staging(self, job_id: str) -> None:
        directory = self._config.staging_root / job_id
        if directory.is_dir():
            shutil.rmtree(directory)

    def _configured_providers(
        self,
    ) -> tuple[MusicBrainzProvider | None, AcoustIdProvider | None, ArtworkProvider | None]:
        if self._config.live_transport is None:
            return self._config.musicbrainz_provider, self._config.acoustid_provider, self._config.artwork_provider
        settings = load_runtime_settings(self._session)
        musicbrainz = (
            MusicBrainzV2Adapter(
                self._config.live_transport, settings.musicbrainz_user_agent, settings.musicbrainz_host
            )
            if settings.musicbrainz_enabled
            else None
        )
        acoustid = (
            AcoustIdV2Adapter(self._config.live_transport, settings.acoustid_client_key)
            if settings.acoustid_enabled and settings.acoustid_client_key
            else None
        )
        artwork = musicbrainz if settings.artwork_enabled else None
        return musicbrainz, acoustid, artwork

    def _timeout_seconds(self) -> float:
        if self._config.live_transport is not None:
            return load_runtime_settings(self._session).timeout_seconds
        return self._config.timeout_seconds

    def _allocate_unsorted_filename(self, suffix: str) -> str:
        allocator = self._config.unsorted_filename_allocator
        if allocator is not None:
            return allocator(suffix)
        return allocate_unsorted_filename(self._session, suffix)

    def _max_attempts(self) -> int:
        if self._config.live_transport is not None:
            return load_runtime_settings(self._session).max_attempts
        return self._config.max_attempts

    def _retry_claim(
        self,
        claimed: ClaimedJob,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        reason = self._history_reason(claimed, reason, error)
        LOGGER.warning(
            'processing job retry',
            extra={'job_id': claimed.job.id, 'attempt': claimed.attempt.attempt_number},
            exc_info=error,
        )
        repository = JobRepository(self._session)
        repository.retry(
            claimed,
            datetime.now(UTC),
            timedelta(seconds=load_runtime_settings(self._session).retry_delay_seconds)
            if self._config.live_transport is not None
            else self._config.retry_delay,
            self._max_attempts(),
            reason,
        )
        if claimed.job.library_record_id is not None:
            if self._session.get(LibraryRecord, claimed.job.library_record_id) is None:
                return
            record_event(
                self._session,
                claimed.job.library_record_id,
                'selection_refresh_retry',
                'retrying',
                reason,
                now,
            )
            return
        source = self._session.get(SourceRecord, claimed.job.source_id)
        if source is None:
            return
        record = ensure_source_record(self._session, source, now)
        blocked = claimed.job.state == 'blocked_infrastructure'
        record_event(
            self._session,
            record.id,
            'processing_blocked' if blocked else 'processing_retry',
            'blocked_infrastructure' if blocked else 'retrying',
            reason,
            now,
            source.id,
        )

    def _history_reason(self, claimed: ClaimedJob, reason: str, error: BaseException | None = None) -> str:
        error_name = type(error).__name__ if error is not None else 'InputValidationError'
        source_id = claimed.job.source_id or 'record-only'
        cause = ''
        if error is not None and error.__cause__ is not None:
            cause = f' caused_by={type(error.__cause__).__name__}: {error.__cause__}'
        return (
            f'job={claimed.job.id} attempt={claimed.attempt.attempt_number} source={source_id}; '
            f'{error_name}: {reason}{cause}'
        )

    def _requeue_changed_source(self, claimed: ClaimedJob, source: SourceRecord, path: Path, now: datetime) -> None:
        record = ensure_source_record(self._session, source, now)
        origin = Origin.LIDARR if source.origin == Origin.LIDARR.value else Origin.MANUAL
        replacement = intake_source(
            self._session,
            IntakeRequest(
                source_path=path,
                source_root_id=source.source_root_id,
                origin=origin,
                duration_seconds=source.duration_seconds,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        replacement_source = self._session.get(SourceRecord, replacement.source_id)
        if replacement_source is None:
            raise ProcessingInfrastructureError('changed source replacement was not persisted')
        orphan_record = replacement_source.library_record
        _ = attach_source(self._session, replacement_source.id, record.id, reason='source_replaced', now=now)
        if orphan_record is not None and orphan_record.id != record.id:
            self._session.delete(orphan_record)
        source.intake_state = 'replaced'
        claimed.attempt.state = 'succeeded'
        claimed.attempt.finished_at = datetime.now(UTC)
        claimed.job.state = 'superseded'
        claimed.job.next_attempt_at = None
        _ = JobRepository(self._session).enqueue(replacement.source_id, 'filesystem_scan', now)

    def _quarantine(
        self,
        claimed: ClaimedJob,
        source: SourceRecord,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        event_type = 'root_boundary' if reason.startswith('root boundary:') else 'processing_quarantined'
        reason = self._history_reason(claimed, reason, error)
        source.intake_state = 'quarantined'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(self._session, record.id, event_type, 'quarantined', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, datetime.now(UTC))

    def _invalid_audio(
        self,
        claimed: ClaimedJob,
        source: SourceRecord,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
        reason = self._history_reason(claimed, reason, error)
        source.intake_state = 'invalid_audio'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(self._session, record.id, 'invalid_audio', 'invalid_audio', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, datetime.now(UTC))
