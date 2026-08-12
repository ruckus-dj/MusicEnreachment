from __future__ import annotations

import json
import logging
import shutil
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import Final, final, override
from uuid import uuid4

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.association import AutomaticAssociationRequest, RecordingAssociationService
from music_ingest.dto import ALLOWED_TAG_KEYS, CandidateEvidencePayload, FieldPolicy, GenrePolicy
from music_ingest.enrichment.artwork import ArtworkProvider, ArtworkWriteRequest, write_release_artwork
from music_ingest.enrichment.fingerprints import FingerprintRequest, FingerprintResult, fingerprint_source
from music_ingest.external.acoustid import AcoustIdV2Adapter
from music_ingest.external.musicbrainz import MusicBrainzV2Adapter
from music_ingest.external.musicbrainz_genres import display_genre_name
from music_ingest.inspectors._tool import ToolState
from music_ingest.inspectors.decoder import DecoderValidationError, validate_decoder
from music_ingest.inspectors.flac import FlacFindingKind, inspect_flac
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
)
from music_ingest.matching.scoring import (
    DEFAULT_CONFIDENCE_THRESHOLD,
    ExplicitMusicBrainzIds,
    MatchDecision,
    MatchingRequest,
    MatchResult,
    resolve_match,
)
from music_ingest.models import (
    ArtworkRecord,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceTagRecord,
    StorageConfigRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.normalize.metadata import (
    CanonicalSource,
    MetadataWriteError,
    MetadataWriteRequest,
    write_canonical_metadata,
    write_observed_metadata,
)
from music_ingest.processing.metadata import (
    fallback_metadata,
    field_policy,
    file_hash,
    genre_policy,
    next_unsorted_filename,
    publication_layout,
    read_tags,
)
from music_ingest.publication import (
    PublicationAttemptRequest,
    expose_attempt,
    finalize_and_cleanup_attempt,
    mark_staged,
    reconcile_attempts,
    reserve_attempt,
)
from music_ingest.publication.service import (
    PublicationError,
    PublicationRequest,
    publish_release,
    replace_published_audio,
)
from music_ingest.sanitizers.flac import FlacSanitizationFailure, FlacSanitizationRequest, sanitize_flac
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
    flac_command: str = 'flac'
    ffmpeg_command: str = 'ffmpeg'
    metaflac_command: str = 'metaflac'
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
        case AcoustIdMatch(evidence=evidence) if 'MUSICBRAINZ_TRACKID' not in analyzed:
            analyzed['MUSICBRAINZ_TRACKID'] = evidence.recording_mbid
        case _:
            pass
    if match_result is not None and match_result.selected_release_mbid is not None:
        analyzed['MUSICBRAINZ_ALBUMID'] = match_result.selected_release_mbid
    return analyzed


def _candidate_tags(candidate: ReleaseCandidate) -> dict[str, str]:
    tags: dict[str, str] = {
        'ALBUM': candidate.release_title,
        'ARTIST': candidate.artist_name,
        'ALBUMARTIST': candidate.artist_name,
        'MUSICBRAINZ_ALBUMID': candidate.release_mbid,
    }
    if candidate.recording_mbids:
        tags['MUSICBRAINZ_TRACKID'] = candidate.recording_mbids[0]
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
        'MUSICBRAINZ_RELEASEGROUPID': candidate.release_group_mbid,
    }
    tags.update({name: value for name, value in optional_tags.items() if value is not None})
    return tags


def _candidate_record(candidate: ReleaseCandidate, score: float | None) -> CandidateRecord:
    return CandidateRecord(
        candidate_key=candidate.release_mbid,
        evidence=json.dumps(
            {
                'provider': 'musicbrainz',
                'artist': candidate.artist_name,
                'release': candidate.release_title,
                'score': score,
                'tags': _candidate_tags(candidate),
            },
            sort_keys=True,
        ),
    )


def _acoustid_recording_mbid(source: SourceRecord) -> str | None:
    for candidate in reversed(source.candidates):
        evidence = CandidateEvidencePayload.model_validate_json(candidate.evidence)
        if evidence.provider == 'acoustid':
            return evidence.tags.get('MUSICBRAINZ_TRACKID')
    return None


def _has_reviewer_decision(source: SourceRecord, state: str) -> bool:
    return any(decision.state == state for decision in source.review_decisions)


def musicbrainz_lookup_ids(record: LibraryRecord, source: SourceRecord) -> tuple[str | None, str | None]:
    recording_mbid = (
        source.association_override.recording_mbid
        if source.association_override is not None
        else (
            record.musicbrainz_recording_id
            if _has_reviewer_decision(source, 'acoustid_confirmed')
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
        if _has_reviewer_decision(source, 'acoustid_confirmed') and record.musicbrainz_recording_id is not None
        else None,
    )


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


def _apply_match_identity(record: LibraryRecord, match_result: MatchResult | None) -> None:
    if match_result is None or match_result.decision is not MatchDecision.AUTO_SELECTED:
        return
    record.match_state = 'matched'
    record.musicbrainz_release_id = match_result.selected_release_mbid
    record.musicbrainz_recording_id = match_result.recording_score.candidate_mbid


@final
class ProcessingWorker:
    def __init__(self, session: Session, config: ProcessingConfig, *, lease_age: timedelta | None = None) -> None:
        self._session: Session = session
        self._config: ProcessingConfig = config
        self._lease_age: timedelta = lease_age or timedelta(minutes=5)

    def run_once(self) -> bool:
        storage = self._session.get(StorageConfigRecord, 1)
        if storage is not None:
            self._config = replace(self._config, media_root=Path(storage.output_root))
        now = datetime.now(UTC)
        reconcile_attempts(self._session, now)
        claimed = JobRepository(self._session).claim_next(now, self._lease_age)
        if claimed is None:
            return False
        try:
            self._process(claimed, now)
        except MetadataWriteError as error:
            self._retry_claim(claimed, str(error), now)
        except ProcessingInfrastructureError as error:
            self._retry_claim(claimed, str(error), now)
        except DecoderValidationError as error:
            source = self._source(claimed)
            self._quarantine(claimed, source, str(error), now)
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
            self._retry_claim(claimed, 'unexpected processing error', now, error)
        finally:
            self._discard_staging(claimed.job.id)
            if claimed.attempt.state == 'running':
                JobRepository(self._session).succeed(claimed, datetime.now(UTC))
        return True

    def _process(self, claimed: ClaimedJob, now: datetime) -> None:
        if claimed.job.kind == 'selection_refresh':
            self._process_selection_refresh(claimed, now)
            return
        if claimed.job.kind in {'acoustid_analysis', 'musicbrainz_analysis'}:
            self._process_analysis(claimed, now)
            return
        if claimed.job.kind == 'final_publish':
            self._process_final_publish(claimed, now)
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
        source = next(item for item in record.sources if item.id == decision.source_id)
        publication = next((item for item in record.publications if item.state == 'current'), None)
        if publication is not None and (
            publication.source_id == decision.source_id
            and publication.metadata_revision_id == revision.id
            and publication.path.casefold().endswith(Path(source.source_path).suffix.casefold())
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
            'publishing',
            f'effective source {decision.source_id} selected for refresh',
            now,
            decision.source_id,
        )

    def _process_initial(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = self._owned_source_path(claimed, source, now)
        if source_path is None:
            return
        capability = inspect_media_capability(source_path, timeout_seconds=self._timeout_seconds()).capability
        if capability is None:
            self._quarantine(claimed, source, 'source has no declared media capability', now)
            return
        is_flac = source_path.suffix.casefold() == '.flac'
        if self._changed(source, source_path):
            self._requeue_changed_source(claimed, source, source_path, now)
            return
        if is_flac:
            inspection = inspect_flac(
                source_path, flac_command=self._config.flac_command, timeout_seconds=self._timeout_seconds()
            )
            malformed = any(finding.kind is FlacFindingKind.MALFORMED_CONTAINER for finding in inspection.findings)
            if malformed:
                self._invalid_audio(claimed, source, 'malformed FLAC container', now)
                return
            has_repairable_wrapper = any(
                finding.kind is FlacFindingKind.TRAILING_ID3V1 for finding in inspection.findings
            )
            if inspection.flac_test.state is ToolState.FAILED and not has_repairable_wrapper:
                self._invalid_audio(claimed, source, 'FLAC decoder rejected audio', now)
                return
            if inspection.flac_test.state is not ToolState.SUCCESS and not has_repairable_wrapper:
                raise ProcessingInfrastructureError(f'flac inspection unavailable: {inspection.flac_test.state}')
            _ = fingerprint_source(
                self._session,
                FingerprintRequest(SourceId(source.id), source_path, inspection),
                fpcalc_command=self._config.fpcalc_command,
                timeout_seconds=self._timeout_seconds(),
            )
        tags = read_tags(source_path, self._config.metaflac_command, self._timeout_seconds())
        self._capture_observations(source, source_path, tags)
        original_tags = dict(tags)
        metadata = fallback_metadata(tags)
        record = ensure_source_record(self._session, source, now)
        _ = append_metadata_revision(self._session, record.id, source.id, 'original', original_tags, 'source', now)
        staged_release = self._staging_directory(claimed.job.id)
        sanitized_path = staged_release / ('.sanitized.flac' if is_flac else f'.staged{source_path.suffix}')
        relative_directory, output_name = publication_layout(tags, source_path.name)
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        if relative_directory == 'Unsorted' and current_publication is None:
            output_name = next_unsorted_filename(
                self._config.media_root / relative_directory, source_path.suffix.casefold()
            )
        if is_flac:
            sanitized = sanitize_flac(
                FlacSanitizationRequest(
                    source_path, sanitized_path, staged_release, self._config.flac_command, self._timeout_seconds()
                )
            )
            source.media_codec = 'FLAC'
            source.media_bit_depth = sanitized.streaminfo.bits_per_sample
            source.media_sample_rate = sanitized.streaminfo.sample_rate
            source.media_channels = sanitized.streaminfo.channels
            source.media_bitrate = None
        else:
            shutil.copy2(source_path, sanitized_path)
            source.media_codec = capability.codec.upper()
        output_path = staged_release / output_name
        if metadata is None and is_flac:
            observed = write_observed_metadata(
                sanitized_path, tags, self._config.metaflac_command, self._timeout_seconds()
            )
            _ = sanitized_path.rename(output_path)
            written = replace(observed, output_path=output_path)
        else:
            if metadata is None:
                raise MetadataWriteError('non-FLAC source requires canonical metadata')
            written = write_canonical_metadata(
                MetadataWriteRequest(
                    sanitized_path,
                    output_path,
                    staged_release,
                    metadata,
                    field_policy(),
                    genre_policy(metadata.genres, load_runtime_settings(self._session)),
                    self._config.metaflac_command,
                    self._timeout_seconds(),
                )
            )
            sanitized_path.unlink()
        self._stage_artwork(source_path, staged_release)
        validate_decoder(
            output_path,
            ffmpeg_command=self._config.ffmpeg_command,
            timeout_seconds=self._timeout_seconds(),
        )
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
        destination_release = (
            Path(current_publication.path).parent
            if current_publication is not None
            else self._config.media_root / relative_directory
        )
        request = PublicationRequest(
            staged_release,
            self._config.staging_root,
            self._config.media_root,
            (source_path,),
            require_canonical_tags=False,
            destination_release=destination_release,
            replace_existing=current_publication is not None,
            destination_audio_name=None if current_publication is None else Path(current_publication.path).name,
            sources=(source,),
        )
        result = (
            publish_release(request)
            if current_publication is None
            else replace_published_audio(request, Path(current_publication.path))
        )
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', dict(written.tags), 'worker', now
        )
        audio_path = result.published_audio
        if audio_path is None:
            audio_path = (
                Path(current_publication.path)
                if current_publication is not None
                else next(result.published_release.glob(f'*{source_path.suffix}'))
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
        configured_musicbrainz, configured_acoustid, _ = self._configured_providers()
        providers_enabled = configured_musicbrainz is not None or configured_acoustid is not None
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
        if not written.tags:
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
        inspection = inspect_flac(
            source_path, flac_command=self._config.flac_command, timeout_seconds=self._timeout_seconds()
        )
        if any(finding.kind is FlacFindingKind.MALFORMED_CONTAINER for finding in inspection.findings):
            self._invalid_audio(claimed, source, 'malformed FLAC container', now)
            return
        if inspection.flac_test.state is not ToolState.SUCCESS:
            raise ProcessingInfrastructureError(f'flac analysis unavailable: {inspection.flac_test.state}')
        fingerprint = fingerprint_source(
            self._session,
            FingerprintRequest(SourceId(source.id), source_path, inspection),
            fpcalc_command=self._config.fpcalc_command,
            timeout_seconds=self._timeout_seconds(),
        )
        tags = read_tags(source_path, self._config.metaflac_command, self._timeout_seconds())
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
            raise ValueError('provider analysis has no configured providers')
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
                _ = self._capture_provider_attempt(source, 'acoustid', acoustic_result, None)
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
        match_result = self._resolve_provider_match(record, source, tags, provider_result)
        _ = self._capture_provider_attempt(source, 'musicbrainz', provider_result.musicbrainz, match_result)
        if match_result is None or match_result.decision is not MatchDecision.AUTO_SELECTED:
            record_event(
                self._session,
                record.id,
                'analysis_ready_for_review',
                'needs_review',
                'AcousticID and MusicBrainz analysis completed without a safe automatic match',
                now,
                source.id,
            )
            return
        recording_mbid = match_result.recording_score.candidate_mbid
        match recording_mbid, provider_result.musicbrainz:
            case str() as verified_recording_mbid, MusicBrainzMatch(candidate=candidate) if (
                verified_recording_mbid in candidate.recording_mbids
            ):
                pass
            case _:
                record_event(
                    self._session,
                    record.id,
                    'analysis_ready_for_review',
                    'needs_review',
                    'automatic match has no verified recording identity',
                    now,
                    source.id,
                )
                return
        associated = RecordingAssociationService(self._session).associate_automatic(
            AutomaticAssociationRequest(
                source.id,
                verified_recording_mbid,
                match_result.recording_score.score,
                self._confidence_threshold(),
                json.dumps(
                    {
                        'release_mbid': match_result.selected_release_mbid,
                        'recording_mbid': verified_recording_mbid,
                        'score': match_result.recording_score.score,
                    },
                    sort_keys=True,
                ),
                now,
            )
        )
        if associated is None:
            return
        record = library_record_detail(self._session, associated.library_record_id)
        analyzed_tags = _analyzed_tags(provider_result, match_result)
        source_tags = {name: value for name, value in tags if name in ALLOWED_TAG_KEYS}
        if not analyzed_tags:
            record_event(
                self._session,
                record.id,
                'analysis_unavailable',
                'needs_review',
                'providers returned no usable metadata',
                now,
                source.id,
            )
            return
        analyzed_revision = append_metadata_revision(
            self._session, record.id, source.id, 'analyzed', analyzed_tags, 'provider', now
        )
        final_tags = _final_tags(source_tags, analyzed_tags, match_result)
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', final_tags, 'provider', now
        )
        _ = JobRepository(self._session).enqueue(source.id, 'final_publish', now, final_revision.id)
        _ = JobRepository(self._session).enqueue_selection_refresh(record.id, now)
        record_event(
            self._session,
            record.id,
            'analysis_ready_for_publish',
            'publishing',
            f'analysis revision {analyzed_revision.id} is ready for final publication',
            now,
            source.id,
        )

    def _process_final_publish(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        record = ensure_source_record(self._session, source, now)
        decision = reevaluate_effective_source_decision(self._session, record.id, now)
        if decision.source_id != source.id:
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
        if publication is not None and (
            publication.source_id == source.id
            and publication.metadata_revision_id == revision.id
            and Path(publication.path).suffix.casefold() == source_path.suffix.casefold()
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
        if relative_directory == 'Unsorted' and publication is None:
            output_name = next_unsorted_filename(
                self._config.media_root / relative_directory, source_path.suffix.casefold()
            )
        destination_release = (
            Path(publication.path).parent if publication is not None else self._config.media_root / relative_directory
        )
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
        if destination_release.is_dir():
            shutil.copytree(destination_release, staged_release)
        else:
            staged_release.mkdir()
        sanitized_path = staged_release / '.sanitized.flac'
        output_path = staged_release / output_name
        output_path.unlink(missing_ok=True)
        _ = sanitize_flac(
            FlacSanitizationRequest(
                source_path, sanitized_path, staged_release, self._config.flac_command, self._timeout_seconds()
            )
        )
        metadata = fallback_metadata(tuple(final_tags.items()), CanonicalSource.REVIEWED_MANUAL)
        if metadata is None:
            observed = write_observed_metadata(
                sanitized_path,
                tuple(final_tags.items()),
                self._config.metaflac_command,
                self._timeout_seconds(),
            )
            _ = sanitized_path.rename(output_path)
            _ = replace(observed, output_path=output_path)
        else:
            _ = write_canonical_metadata(
                MetadataWriteRequest(
                    sanitized_path,
                    output_path,
                    staged_release,
                    metadata,
                    field_policy(),
                    genre_policy(metadata.genres, load_runtime_settings(self._session)),
                    self._config.metaflac_command,
                    self._timeout_seconds(),
                )
            )
            sanitized_path.unlink()
        provider_artwork_staged = self._stage_artwork_for_release(source_path, staged_release, final_tags)
        _ = provider_artwork_staged
        mark_staged(self._session, attempt, now)
        expose_attempt(self._session, attempt, now)
        _ = finalize_and_cleanup_attempt(self._session, attempt, now)
        source.intake_state = 'present'
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(self._session, record.id, 'final_published', 'complete', None, now, source.id)

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
        query = f'artist:{values["ARTIST"]} release:{values["ALBUM"]}' if {'ARTIST', 'ALBUM'} <= values.keys() else ''
        configured_musicbrainz, configured_acoustid, _ = self._configured_providers()
        musicbrainz = configured_musicbrainz if run_musicbrainz and query else None
        acoustid = (
            configured_acoustid
            if run_acoustid and fingerprint.fingerprint is not None and fingerprint.duration_seconds is not None
            else None
        )
        if musicbrainz is None and acoustid is None:
            return None
        return ProviderEvidenceService(self._session, musicbrainz, acoustid).lookup(
            ProviderEvidenceRequest(
                query,
                FixtureCase.SUCCESS,
                fingerprint.fingerprint,
                FixtureCase.SUCCESS if acoustid is not None else None,
                duration_seconds=fingerprint.duration_seconds,
                force_refresh=force_refresh,
                release_title=values.get('ALBUM'),
                acoustid_confidence_threshold=self._confidence_threshold(),
                artist_name=values.get('ARTIST'),
                recording_mbid=recording_mbid,
                release_mbid=release_mbid,
                run_acoustid=run_acoustid,
                run_musicbrainz=run_musicbrainz,
            ),
            now,
        )

    def _resolve_provider_match(
        self,
        record: LibraryRecord,
        source: SourceRecord,
        tags: tuple[tuple[str, str], ...],
        result: ProviderEvidenceResult | None,
    ) -> MatchResult | None:
        if result is None:
            return None
        values = {name: value for name, value in tags}
        return resolve_match(
            MatchingRequest(
                values.get('ARTIST', ''),
                values.get('ALBUM', ''),
                source.duration_seconds,
                _reviewer_selected_musicbrainz_ids(record, source),
            ),
            result.musicbrainz,
            result.acoustid,
            self._confidence_threshold(),
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
        match_result: MatchResult | None,
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
                    )
                )
                candidate_scores = (
                    {}
                    if match_result is None
                    else {item.candidate_mbid: item.score for item in match_result.candidate_scores}
                )
                match result:
                    case MusicBrainzMatch(candidate=candidate):
                        source.candidates.append(
                            _candidate_record(candidate, candidate_scores.get(candidate.release_mbid))
                        )
                    case Ambiguous(candidates=candidates):
                        source.candidates.extend(
                            _candidate_record(candidate, candidate_scores.get(candidate.release_mbid))
                            for candidate in candidates
                        )
                    case AcoustIdMatch(evidence=evidence):
                        recordings = evidence.candidates or (
                            RecordingCandidate(evidence.recording_mbid, evidence.score),
                        )
                        source.candidates.extend(
                            CandidateRecord(
                                candidate_key=recording.recording_mbid,
                                evidence=json.dumps(
                                    {
                                        'provider': 'acoustid',
                                        'recording_mbid': recording.recording_mbid,
                                        'score': recording.score,
                                        'artist': '',
                                        'release': '',
                                        'title': '',
                                        'album': '',
                                        'tags': {'MUSICBRAINZ_TRACKID': recording.recording_mbid},
                                    },
                                    sort_keys=True,
                                ),
                            )
                            for recording in recordings
                        )
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
        stat = path.stat()
        return (stat.st_dev, stat.st_ino, stat.st_size, file_hash(path)) != (
            source.device,
            source.inode,
            source.size_bytes,
            source.sha256,
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
        self._config.media_root.mkdir(parents=True, exist_ok=True)
        if directory.exists():
            shutil.rmtree(directory)
        directory.mkdir()
        return directory

    def _discard_staging(self, job_id: str) -> None:
        directory = self._config.staging_root / job_id
        if directory.is_dir():
            shutil.rmtree(directory)

    def _stage_artwork(self, source_path: Path, staged_release: Path) -> None:
        artwork = next(
            (path for path in (source_path.parent / 'cover.jpg', source_path.parent / 'cover.webp') if path.is_file()),
            None,
        )
        if artwork is None:
            return
        _ = shutil.copy2(artwork, staged_release / artwork.name)

    def _stage_artwork_for_release(self, source_path: Path, staged_release: Path, final_tags: dict[str, str]) -> bool:
        self._stage_artwork(source_path, staged_release)
        release_id = final_tags.get('MUSICBRAINZ_ALBUMID')
        _, _, provider = self._configured_providers()
        if release_id is None or provider is None:
            return False
        setting_key = f'artwork:{release_id}'
        if self._session.get(RuntimeSettingRecord, setting_key) is not None:
            return False
        candidate = provider.fetch_artwork(release_id)
        setting_value = 'missing'
        if candidate is not None:
            for name in ('cover.jpg', 'cover.webp'):
                (staged_release / name).unlink(missing_ok=True)
            _ = write_release_artwork(
                ArtworkWriteRequest(self._config.staging_root, staged_release, release_id, candidate)
            )
            setting_value = candidate.format.value
        self._session.add(RuntimeSettingRecord(key=setting_key, value=setting_value, updated_at=datetime.now(UTC)))
        return candidate is not None

    def _configured_providers(
        self,
    ) -> tuple[MusicBrainzProvider | None, AcoustIdProvider | None, ArtworkProvider | None]:
        if self._config.live_transport is None:
            return self._config.musicbrainz_provider, self._config.acoustid_provider, self._config.artwork_provider
        settings = load_runtime_settings(self._session)
        musicbrainz = (
            MusicBrainzV2Adapter(self._config.live_transport, settings.musicbrainz_user_agent)
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

    def _retry_claim(
        self,
        claimed: ClaimedJob,
        reason: str,
        now: datetime,
        error: BaseException | None = None,
    ) -> None:
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
            load_runtime_settings(self._session).max_attempts
            if self._config.live_transport is not None
            else self._config.max_attempts,
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

    def _quarantine(self, claimed: ClaimedJob, source: SourceRecord, reason: str, now: datetime) -> None:
        source.intake_state = 'quarantined'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        event_type = 'root_boundary' if reason.startswith('root boundary:') else 'processing_quarantined'
        record_event(self._session, record.id, event_type, 'quarantined', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, datetime.now(UTC))

    def _invalid_audio(self, claimed: ClaimedJob, source: SourceRecord, reason: str, now: datetime) -> None:
        source.intake_state = 'invalid_audio'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        _ = reevaluate_effective_source_decision(self._session, record.id, now)
        record_event(self._session, record.id, 'invalid_audio', 'invalid_audio', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, datetime.now(UTC))
