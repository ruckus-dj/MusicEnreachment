from __future__ import annotations

import json
import logging
import shutil
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import Final, final, override
from uuid import uuid4

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session, raiseload, selectinload

from music_ingest.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.dto import ALLOWED_TAG_KEYS, CandidateEvidencePayload, FieldPolicy, GenrePolicy, RuntimeSettings
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
from music_ingest.inspectors._tool import ToolEvidence
from music_ingest.inspectors.decoder import DecoderValidationError, validate_decoder
from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.intake.service import IntakeRequest, Origin, SourceId, intake_source
from music_ingest.library.service import (
    append_metadata_revision,
    attach_source,
    ensure_source_record,
    library_record_detail,
    new_library_record,
    record_event,
    record_publication,
    reevaluate_effective_source_decision,
)
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceResult, ProviderEvidenceService
from music_ingest.matching.musicbrainz import MusicBrainzProviderAdapter
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
    score_recording_release_candidate,
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
from music_ingest.normalize.genre_names import display_genre_name
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
from music_ingest.settings import SettingKey, build_runtime_settings, get_setting_value, get_setting_values
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
    _release_score: CandidateScore | None,
    request: MatchingRequest | None,
    source: SourceRecord | None = None,
) -> tuple[CandidateRecord, ...]:
    recording_candidates = candidate.recording_candidates or (candidate,)
    return tuple(
        CandidateRecord(
            source_id=source_id,
            candidate_key=f'{recording_candidate.release_mbid}:{recording_mbid}',
            evidence=json.dumps(
                {
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
                    'score_components': (
                        None
                        if unified_score is None
                        else {
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
                    ),
                    'release_mbid': recording_candidate.release_mbid,
                    'recording_mbid': recording_mbid,
                    'compatible_ids': (),
                    'tags': _candidate_tags(recording_candidate),
                },
                sort_keys=True,
            ),
        )
        for recording_candidate in recording_candidates
        for recording_mbid in recording_candidate.recording_mbids
        for unified_score in (
            score_recording_release_candidate(
                request,
                recording_candidate,
                None if source is None else _acoustid_recording_score(source, recording_mbid),
            )
            if request is not None
            else None,
        )
    )


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


def _release_candidates_for_recording(
    recording_mbid: str, candidates: Iterable[ReleaseCandidate]
) -> tuple[ReleaseCandidate, ...]:
    return tuple(candidate for candidate in candidates if recording_mbid in candidate.recording_mbids)


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
        if claimed.job.source_id is not None:
            source = self._session.get(SourceRecord, claimed.job.source_id)
            if source is not None and source.intake_state == 'replaced':
                claimed.attempt.state = 'succeeded'
                claimed.attempt.finished_at = now
                claimed.job.state = 'superseded'
                claimed.job.next_attempt_at = None
                return
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
            # Two-stage naming: the durable Unsorted filename is only allocated below, after
            # process_media() has produced output, because allocation must not reserve a name for
            # audio that could still fail to stage. Use a job-scoped hidden name as the pipeline's
            # working output name in the meantime; it is never exposed to callers.
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
                build_runtime_settings(self._session, include_genres=True) if metadata is not None else None,
                cached_fingerprint is None,
            )
        )
        if pipeline_result.fingerprint is not None and cached_fingerprint is None:
            persist_fingerprint(self._session, SourceId(source.id), pipeline_result.fingerprint)
        source.media_codec = capability.codec.upper()
        written_tags = pipeline_result.written_tags
        self._session.flush()
        if unsorted_destination:
            # Staging succeeded; now allocate the real, durable Unsorted filename to replace the
            # hidden working name chosen above.
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
            # Deliberate: initial publication is not gated on having any tags at all. The audio is
            # already published above so it is reviewable immediately; this event only flags that
            # metadata still needs to be supplied, matching the "keep unresolved matches reviewable
            # rather than blocking" policy rather than a stricter "no publication without tags" rule.
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
        _ = self._session.scalar(select(SourceRecord).where(SourceRecord.id == source.id).with_for_update())
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
        recording_mbids = tuple(
            dict.fromkeys((*_acoustid_recording_mbids(source), *((recording_mbid,) if recording_mbid else ())))
        )
        provider_result = self._lookup_providers(
            tags,
            fingerprint,
            now,
            force_refresh=True,
            recording_mbid=recording_mbid,
            release_mbid=release_mbid,
            recording_mbids=recording_mbids,
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
        _ = self._capture_provider_attempt(
            source,
            'musicbrainz',
            provider_result.musicbrainz,
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
                build_runtime_settings(self._session, include_genres=True) if metadata is not None else None,
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
        value = get_setting_value(self._session, SettingKey.ARTWORK_ENABLED)
        return value is None or value.casefold() == 'true'

    def _lookup_providers(
        self,
        tags: tuple[tuple[str, str], ...],
        fingerprint: FingerprintResult,
        now: datetime,
        force_refresh: bool = False,
        recording_mbid: str | None = None,
        release_mbid: str | None = None,
        recording_mbids: tuple[str, ...] = (),
        run_acoustid: bool = True,
        run_musicbrainz: bool = True,
    ) -> ProviderEvidenceResult | None:
        values = {name: value for name, value in tags}
        query = ' '.join(
            f'{field}:"{value}"'
            for field, value in (
                ('artist', values.get('ARTIST')),
                ('release', values.get('ALBUM')),
                ('recording', values.get('TITLE')),
            )
            if value
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
                recording_mbids=recording_mbids,
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
        source = self._candidate_selection_source(claimed)
        if source.library_record_id is None:
            record = new_library_record(self._session, now)
            source.library_record = record
            self._session.flush()
        self._enqueue_folder_selection_if_ready(source, claimed.job.id, now)

    def _enqueue_folder_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        folder = _folder_selection_root(source.source_path)
        members = self._folder_members(folder)
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
                    ]
                )
            )
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.id != current_job_id)
        )
        if active_collection is not None:
            return
        _ = JobRepository(self._session).enqueue_folder_release_selection(str(folder), now)

    def _process_folder_release_selection(self, claimed: ClaimedJob, now: datetime) -> None:
        folder_path = claimed.job.folder_path
        if folder_path is None:
            raise ProcessingInfrastructureError('folder release selection requires a folder target')
        folder = Path(folder_path)
        members = self._folder_members(folder)
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
        selected_release = select_folder_release(groups, self._confidence_threshold())
        if selected_release is None:
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
            match = _stored_release_candidate(source, selected_release)
            if match is None:
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
            recording_mbid, match_evidence = match[1].recording_mbid, match[1]
            if recording_mbid is None:
                continue
            associated = RecordingAssociationService(self._session).associate_automatic(
                AutomaticAssociationRequest(
                    source.id,
                    recording_mbid,
                    match_evidence.score or 0.0,
                    self._confidence_threshold(),
                    json.dumps({'recording_mbid': recording_mbid, 'release_mbid': selected_release}, sort_keys=True),
                    now,
                    release_mbid=selected_release,
                )
            )
            if associated is None:
                continue
            record = library_record_detail(self._session, associated.library_record_id)
            analyzed_tags = _stored_match_tags(None, match)
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
            value = get_setting_value(self._session, SettingKey.CONFIDENCE_THRESHOLD)
            if value is None:
                return self._config.confidence_threshold
            try:
                parsed = float(value)
            except ValueError:
                return self._config.confidence_threshold
            return parsed if 0.0 <= parsed <= 1.0 else self._config.confidence_threshold
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
                match result:
                    case MusicBrainzMatch(candidate=candidate):
                        candidate_records = _candidate_records(source.id, candidate, None, request, source)
                        source.candidates.extend(candidate_records)
                        run.candidates.extend(candidate_records)
                    case Ambiguous(candidates=candidates):
                        candidate_records = tuple(
                            record
                            for candidate in candidates
                            for record in _candidate_records(source.id, candidate, None, request, source)
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

    def _candidate_selection_source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self._session.scalar(
            select(SourceRecord)
            .where(SourceRecord.id == claimed.job.source_id)
            .options(
                raiseload('*'),
                selectinload(SourceRecord.candidates),
                selectinload(SourceRecord.candidate_runs).selectinload(ProviderCandidateRunRecord.candidates),
                selectinload(SourceRecord.tag_observations),
            )
        )
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def _folder_members(self, folder: Path) -> tuple[SourceRecord, ...]:
        members = self._session.scalars(
            select(SourceRecord)
            .where(SourceRecord.source_path.startswith(f'{folder}/', autoescape=True))
            .where(SourceRecord.library_record_id.is_not(None))
            .where(SourceRecord.disappeared_at.is_(None))
            .where(SourceRecord.intake_state != 'replaced')
            .options(
                raiseload('*'),
                selectinload(SourceRecord.candidates),
                selectinload(SourceRecord.candidate_runs).selectinload(ProviderCandidateRunRecord.candidates),
                selectinload(SourceRecord.tag_observations),
            )
        ).all()
        return tuple(item for item in members if _folder_selection_root(item.source_path) == folder)

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
        settings = get_setting_values(
            self._session,
            (
                SettingKey.MUSICBRAINZ_ENABLED,
                SettingKey.MUSICBRAINZ_USER_AGENT,
                SettingKey.MUSICBRAINZ_HOST,
                SettingKey.ACOUSTID_ENABLED,
                SettingKey.ACOUSTID_CLIENT_KEY,
                SettingKey.ARTWORK_ENABLED,
            ),
        )
        defaults = RuntimeSettings()
        musicbrainz = (
            MusicBrainzProviderAdapter(
                self._config.live_transport,
                settings.get(SettingKey.MUSICBRAINZ_USER_AGENT, defaults.musicbrainz_user_agent),
                settings.get(SettingKey.MUSICBRAINZ_HOST, defaults.musicbrainz_host),
            )
            if settings.get(SettingKey.MUSICBRAINZ_ENABLED, str(defaults.musicbrainz_enabled).lower()) == 'true'
            else None
        )
        acoustid = (
            AcoustIdV2Adapter(self._config.live_transport, client_key)
            if settings.get(SettingKey.ACOUSTID_ENABLED, str(defaults.acoustid_enabled).lower()) == 'true'
            and (client_key := settings.get(SettingKey.ACOUSTID_CLIENT_KEY, ''))
            else None
        )
        artwork = (
            musicbrainz
            if settings.get(SettingKey.ARTWORK_ENABLED, str(defaults.artwork_enabled).lower()) == 'true'
            else None
        )
        return musicbrainz, acoustid, artwork

    def _timeout_seconds(self) -> float:
        if self._config.live_transport is not None:
            value = get_setting_value(self._session, SettingKey.TIMEOUT_SECONDS)
            if value is not None:
                try:
                    return float(value)
                except ValueError:
                    pass
        return self._config.timeout_seconds

    def _allocate_unsorted_filename(self, suffix: str) -> str:
        allocator = self._config.unsorted_filename_allocator
        if allocator is not None:
            return allocator(suffix)
        return allocate_unsorted_filename(self._session, suffix)

    def _max_attempts(self) -> int:
        if self._config.live_transport is not None:
            value = get_setting_value(self._session, SettingKey.MAX_ATTEMPTS)
            if value is not None:
                try:
                    return int(value)
                except ValueError:
                    pass
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
            timedelta(
                seconds=float(
                    get_setting_value(self._session, SettingKey.RETRY_DELAY_SECONDS)
                    or self._config.retry_delay.total_seconds()
                )
            )
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
        source.replaced_by_source_id = replacement_source.id
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
