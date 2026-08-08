from __future__ import annotations

import json
import logging
import shutil
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import final, override

from pydantic import TypeAdapter
from sqlalchemy.orm import Session

from music_ingest.config.policies import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.enrichment.fingerprints import FingerprintRequest, FingerprintResult, fingerprint_source
from music_ingest.inspectors._tool import ToolState
from music_ingest.inspectors.flac import FlacFindingKind, inspect_flac
from music_ingest.intake.service import IntakeRequest, Origin, SourceId, intake_source
from music_ingest.library.service import (
    append_metadata_revision,
    attach_source,
    ensure_source_record,
    record_event,
    record_publication,
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
    Malformed,
    MusicBrainzMatch,
    MusicBrainzProvider,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
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
from music_ingest.normalize.metadata import (
    CanonicalSource,
    MetadataWriteError,
    MetadataWriteRequest,
    write_canonical_metadata,
    write_observed_metadata,
)
from music_ingest.persistence.jobs import ClaimedJob, JobRepository
from music_ingest.persistence.library import LibraryRecord
from music_ingest.persistence.models import (
    ArtworkRecord,
    CandidateRecord,
    ProviderAttemptRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceTagRecord,
)
from music_ingest.processing.metadata import _fallback_metadata, _field_policy, _genre_policy, _hash, _read_tags
from music_ingest.publication.service import (
    PublicationError,
    PublicationRequest,
    publish_release,
)
from music_ingest.sanitizers.flac import FlacSanitizationFailure, FlacSanitizationRequest, sanitize_flac

LOGGER = logging.getLogger(__name__)
_TAGS_ADAPTER = TypeAdapter(dict[str, str])


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
    metaflac_command: str = 'metaflac'
    fpcalc_command: str = 'fpcalc'
    timeout_seconds: float = 10.0
    retry_delay: timedelta = timedelta(seconds=30)
    max_attempts: int = 3
    field_policy: FieldPolicy | None = None
    genre_policy: GenrePolicy | None = None
    musicbrainz_provider: MusicBrainzProvider | None = None
    acoustid_provider: AcoustIdProvider | None = None
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
            analyzed['ALBUM'] = candidate.release_title
            analyzed['ARTIST'] = candidate.artist_name
            analyzed['ALBUMARTIST'] = candidate.artist_name
            analyzed['MUSICBRAINZ_ALBUMID'] = candidate.release_mbid
            if candidate.recording_mbids:
                analyzed['MUSICBRAINZ_TRACKID'] = candidate.recording_mbids[0]
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


def _final_tags(
    source_tags: dict[str, str],
    analyzed_tags: dict[str, str],
    provider_result: ProviderEvidenceResult | None,
    match_result: MatchResult | None,
) -> dict[str, str]:
    final = dict(source_tags)
    musicbrainz_match = False
    if provider_result is not None:
        match provider_result.musicbrainz:
            case MusicBrainzMatch():
                musicbrainz_match = True
            case _:
                pass
    verified_analysis = musicbrainz_match or (
        match_result is not None and match_result.decision is MatchDecision.AUTO_SELECTED
    )
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
        now = datetime.now(UTC)
        claimed = JobRepository(self._session).claim_next(now, self._lease_age)
        if claimed is None:
            return False
        try:
            self._process(claimed, now)
        except MetadataWriteError as error:
            self._retry_claim(claimed, str(error), now)
        except ProcessingInfrastructureError as error:
            self._retry_claim(claimed, str(error), now)
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
        return True

    def _process(self, claimed: ClaimedJob, now: datetime) -> None:
        if claimed.job.kind in {'provider_analysis', 'provider_retry'}:
            self._process_provider_analysis(claimed, now)
            return
        if claimed.job.kind == 'final_publish':
            self._process_final_publish(claimed, now)
            return
        self._process_initial(claimed, now)

    def _process_initial(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = Path(source.source_path).resolve(strict=True)
        valid_source = source_path.suffix.casefold() == '.flac' and source_path.is_relative_to(
            self._config.incoming_root.resolve()
        )
        if not valid_source:
            self._quarantine(claimed, source, 'source path is outside incoming FLAC boundary', now)
            return
        if self._changed(source, source_path):
            self._requeue_changed_source(claimed, source, source_path, now)
            return
        inspection = inspect_flac(
            source_path, flac_command=self._config.flac_command, timeout_seconds=self._config.timeout_seconds
        )
        malformed = any(finding.kind is FlacFindingKind.MALFORMED_CONTAINER for finding in inspection.findings)
        if malformed:
            self._invalid_audio(claimed, source, 'malformed FLAC container', now)
            return
        has_repairable_wrapper = any(finding.kind is FlacFindingKind.TRAILING_ID3V1 for finding in inspection.findings)
        if inspection.flac_test.state is ToolState.FAILED and not has_repairable_wrapper:
            self._invalid_audio(claimed, source, 'FLAC decoder rejected audio', now)
            return
        if inspection.flac_test.state is not ToolState.SUCCESS and not has_repairable_wrapper:
            raise ProcessingInfrastructureError(f'flac inspection unavailable: {inspection.flac_test.state}')
        _ = fingerprint_source(
            self._session,
            FingerprintRequest(SourceId(source.id), source_path, inspection),
            fpcalc_command=self._config.fpcalc_command,
            timeout_seconds=self._config.timeout_seconds,
        )
        tags = _read_tags(source_path, self._config.metaflac_command, self._config.timeout_seconds)
        self._capture_observations(source, source_path, tags)
        original_tags = dict(tags)
        metadata = _fallback_metadata(tags)
        record = ensure_source_record(self._session, source, now)
        _ = append_metadata_revision(self._session, record.id, source.id, 'original', original_tags, 'source', now)
        staged_release = self._staging_directory(claimed.job.id)
        sanitized_path = staged_release / '.sanitized.flac'
        _ = sanitize_flac(
            FlacSanitizationRequest(
                source_path, sanitized_path, staged_release, self._config.flac_command, self._config.timeout_seconds
            )
        )
        output_path = staged_release / source_path.name
        if metadata is None:
            observed = write_observed_metadata(
                sanitized_path, tags, self._config.metaflac_command, self._config.timeout_seconds
            )
            _ = sanitized_path.rename(output_path)
            written = replace(observed, output_path=output_path)
        else:
            written = write_canonical_metadata(
                MetadataWriteRequest(
                    sanitized_path,
                    output_path,
                    staged_release,
                    metadata,
                    self._config.field_policy or _field_policy(),
                    self._config.genre_policy or _genre_policy(metadata.genres),
                    self._config.metaflac_command,
                    self._config.timeout_seconds,
                )
            )
        self._stage_artwork(source_path, staged_release)
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
            JobRepository(self._session).succeed(claimed, now)
            return
        current_publication = next((item for item in record.publications if item.state == 'current'), None)
        result = publish_release(
            PublicationRequest(
                staged_release,
                self._config.staging_root,
                self._config.media_root,
                (source_path,),
                require_canonical_tags=False,
                destination_release=Path(current_publication.path).parent if current_publication is not None else None,
                replace_existing=current_publication is not None,
            )
        )
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', dict(written.tags), 'worker', now
        )
        audio_path = next(result.published_release.glob('*.flac'))
        _ = record_publication(
            self._session,
            record.id,
            source.id,
            audio_path,
            _hash(audio_path),
            final_revision.id,
            now,
        )
        source.intake_state = 'present'
        providers_enabled = self._config.musicbrainz_provider is not None or self._config.acoustid_provider is not None
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
            _ = JobRepository(self._session).enqueue(source.id, 'provider_analysis', now)
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
        JobRepository(self._session).succeed(claimed, now)

    def _process_provider_analysis(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = Path(source.source_path).resolve(strict=True)
        inspection = inspect_flac(
            source_path, flac_command=self._config.flac_command, timeout_seconds=self._config.timeout_seconds
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
            timeout_seconds=self._config.timeout_seconds,
        )
        tags = _read_tags(source_path, self._config.metaflac_command, self._config.timeout_seconds)
        provider_result = self._lookup_providers(tags, fingerprint, now, force_refresh=True)
        if provider_result is None:
            raise ValueError('provider analysis has no configured providers')
        self._capture_provider_evidence(source, provider_result)
        match_result = self._resolve_provider_match(source, tags, provider_result)
        analyzed_tags = _analyzed_tags(provider_result, match_result)
        record = ensure_source_record(self._session, source, now)
        source_tags = {name: value for name, value in tags if name in ALLOWED_TAG_KEYS}
        if not analyzed_tags:
            record_event(
                self._session,
                record.id,
                'provider_analysis_unavailable',
                'needs_review',
                'providers returned no usable metadata',
                now,
                source.id,
            )
            JobRepository(self._session).succeed(claimed, now)
            return
        analyzed_revision = append_metadata_revision(
            self._session, record.id, source.id, 'analyzed', analyzed_tags, 'provider', now
        )
        final_tags = _final_tags(source_tags, analyzed_tags, provider_result, match_result)
        final_revision = append_metadata_revision(
            self._session, record.id, source.id, 'final', final_tags, 'provider', now
        )
        _apply_match_identity(record, match_result)
        _ = JobRepository(self._session).enqueue(source.id, 'final_publish', now, final_revision.id)
        record_event(
            self._session,
            record.id,
            'provider_analysis_ready',
            'publishing',
            f'provider analysis revision {analyzed_revision.id} is ready',
            now,
            source.id,
        )
        JobRepository(self._session).succeed(claimed, now)

    def _process_final_publish(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        record = ensure_source_record(self._session, source, now)
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
        source_path = Path(source.source_path).resolve(strict=True)
        staged_release = self._staging_directory(claimed.job.id)
        sanitized_path = staged_release / '.sanitized.flac'
        _ = sanitize_flac(
            FlacSanitizationRequest(
                source_path, sanitized_path, staged_release, self._config.flac_command, self._config.timeout_seconds
            )
        )
        output_path = staged_release / source_path.name
        metadata = _fallback_metadata(tuple(final_tags.items()), CanonicalSource.REVIEWED_MANUAL)
        if metadata is None:
            observed = write_observed_metadata(
                sanitized_path,
                tuple(final_tags.items()),
                self._config.metaflac_command,
                self._config.timeout_seconds,
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
                    self._config.field_policy or _field_policy(),
                    self._config.genre_policy or _genre_policy(metadata.genres),
                    self._config.metaflac_command,
                    self._config.timeout_seconds,
                )
            )
            sanitized_path.unlink()
        self._stage_artwork(source_path, staged_release)
        publication = next((item for item in record.publications if item.state == 'current'), None)
        request = PublicationRequest(
            staged_release,
            self._config.staging_root,
            self._config.media_root,
            (source_path,),
            require_canonical_tags=False,
            destination_release=Path(publication.path).parent if publication is not None else None,
            replace_existing=publication is not None,
        )
        published = publish_release(request)
        audio_path = next(published.published_release.glob('*.flac'))
        _ = record_publication(
            self._session,
            record.id,
            source.id,
            audio_path,
            _hash(audio_path),
            revision.id,
            now,
        )
        source.intake_state = 'present'
        record_event(self._session, record.id, 'final_published', 'complete', None, now, source.id)
        JobRepository(self._session).succeed(claimed, now)

    def _lookup_providers(
        self,
        tags: tuple[tuple[str, str], ...],
        fingerprint: FingerprintResult,
        now: datetime,
        force_refresh: bool = False,
    ) -> ProviderEvidenceResult | None:
        values = {name: value for name, value in tags}
        query = f'artist:{values["ARTIST"]} release:{values["ALBUM"]}' if {'ARTIST', 'ALBUM'} <= values.keys() else ''
        musicbrainz = self._config.musicbrainz_provider if query else None
        acoustid = (
            self._config.acoustid_provider
            if fingerprint.fingerprint is not None and fingerprint.duration_seconds is not None
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
            ),
            now,
        )

    def _resolve_provider_match(
        self, source: SourceRecord, tags: tuple[tuple[str, str], ...], result: ProviderEvidenceResult | None
    ) -> MatchResult | None:
        if result is None:
            return None
        values = {name: value for name, value in tags}
        return resolve_match(
            MatchingRequest(
                values.get('ARTIST', ''),
                values.get('ALBUM', ''),
                source.duration_seconds,
                ExplicitMusicBrainzIds(),
            ),
            result.musicbrainz,
            result.acoustid,
            self._confidence_threshold(),
        )

    def _confidence_threshold(self) -> float:
        setting = self._session.get(RuntimeSettingRecord, 'matching.confidence_threshold')
        if setting is None:
            return self._config.confidence_threshold
        try:
            value = float(setting.value)
        except ValueError:
            return self._config.confidence_threshold
        return value if 0.0 <= value <= 1.0 else self._config.confidence_threshold

    def _capture_provider_evidence(self, source: SourceRecord, result: ProviderEvidenceResult) -> None:
        self._capture_provider_attempt(source, 'musicbrainz', result.musicbrainz)
        if result.acoustid is not None:
            self._capture_provider_attempt(source, 'acoustid', result.acoustid)

    def _capture_provider_attempt(
        self, source: SourceRecord, provider_name: str, result: MusicBrainzResult | AcoustIdResult
    ) -> None:
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
                match result:
                    case MusicBrainzMatch(candidate=candidate):
                        source.candidates.append(
                            CandidateRecord(
                                candidate_key=candidate.release_mbid,
                                evidence=json.dumps(
                                    {'artist': candidate.artist_name, 'release': candidate.release_title},
                                    sort_keys=True,
                                ),
                            )
                        )
                    case AcoustIdMatch(evidence=evidence):
                        source.candidates.append(
                            CandidateRecord(
                                candidate_key=evidence.recording_mbid,
                                evidence=json.dumps({'score': evidence.score}, sort_keys=True),
                            )
                        )
                    case NoMatch() | Ambiguous() | Disabled() | Malformed() | RateLimited() | Timeout() | Unavailable():
                        return

    def _source(self, claimed: ClaimedJob) -> SourceRecord:
        if claimed.job.source_id is None:
            raise ValueError('processing job has no source')
        source = self._session.get(SourceRecord, claimed.job.source_id)
        if source is None:
            raise ValueError('processing job source is missing')
        return source

    def _changed(self, source: SourceRecord, path: Path) -> bool:
        stat = path.stat()
        return (stat.st_dev, stat.st_ino, stat.st_size, _hash(path)) != (
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
                source.artwork_observations.append(ArtworkRecord(sha256=_hash(artwork)))

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
            self._config.retry_delay,
            self._config.max_attempts,
            reason,
        )
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
        claimed.attempt.finished_at = now
        claimed.job.state = 'superseded'
        claimed.job.next_attempt_at = None
        _ = JobRepository(self._session).enqueue(replacement.source_id, 'filesystem_scan', now)

    def _quarantine(self, claimed: ClaimedJob, source: SourceRecord, reason: str, now: datetime) -> None:
        source.intake_state = 'quarantined'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        record_event(self._session, record.id, 'processing_quarantined', 'quarantined', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, now)

    def _invalid_audio(self, claimed: ClaimedJob, source: SourceRecord, reason: str, now: datetime) -> None:
        source.intake_state = 'invalid_audio'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        record_event(self._session, record.id, 'invalid_audio', 'invalid_audio', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, now)
