from __future__ import annotations

import json
import logging
import shutil
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from subprocess import CalledProcessError, TimeoutExpired
from typing import final

from sqlalchemy.orm import Session

from music_ingest.config.policies import FieldPolicy, GenrePolicy
from music_ingest.enrichment.fingerprints import FingerprintRequest, FingerprintResult, fingerprint_source
from music_ingest.inspectors.flac import InspectionState, inspect_flac
from music_ingest.intake.service import SourceId
from music_ingest.library.service import (
    ensure_source_record,
    record_event,
    record_metadata_layers,
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
)
from music_ingest.persistence.jobs import ClaimedJob, JobRepository
from music_ingest.persistence.models import (
    ArtworkRecord,
    CandidateRecord,
    ProviderAttemptRecord,
    ReviewDecisionRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceTagRecord,
)
from music_ingest.processing.metadata import _fallback_metadata, _field_policy, _genre_policy, _hash, _read_tags
from music_ingest.publication.service import PublicationError, PublicationRequest, publish_release
from music_ingest.sanitizers.flac import FlacSanitizationFailure, FlacSanitizationRequest, sanitize_flac

LOGGER = logging.getLogger(__name__)


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
        except ValueError as error:
            self._quarantine_invalid_claim(claimed, str(error), now)
        except MetadataWriteError as error:
            self._quarantine_invalid_claim(claimed, str(error), now)
        except (
            OSError,
            PublicationError,
            FlacSanitizationFailure,
            CalledProcessError,
            TimeoutExpired,
        ) as error:
            LOGGER.warning(
                'processing job retry',
                extra={'job_id': claimed.job.id, 'attempt': claimed.attempt.attempt_number},
                exc_info=error,
            )
            JobRepository(self._session).retry(
                claimed,
                datetime.now(UTC),
                self._config.retry_delay,
                self._config.max_attempts,
                str(error),
            )
            source = self._session.get(SourceRecord, claimed.job.source_id)
            if source is not None:
                record = ensure_source_record(self._session, source, now)
                record_event(self._session, record.id, 'processing_retry', 'retrying', str(error), now, source.id)
        finally:
            self._discard_staging(claimed.job.id)
        return True

    def _process(self, claimed: ClaimedJob, now: datetime) -> None:
        source = self._source(claimed)
        source_path = Path(source.source_path).resolve(strict=True)
        valid_source = source_path.suffix.casefold() == '.flac' and source_path.is_relative_to(
            self._config.incoming_root.resolve()
        )
        if not valid_source:
            self._quarantine(claimed, source, 'source path is outside incoming FLAC boundary', now)
            return
        if self._changed(source, source_path):
            self._quarantine(claimed, source, 'source changed after intake', now)
            return
        inspection = inspect_flac(
            source_path, flac_command=self._config.flac_command, timeout_seconds=self._config.timeout_seconds
        )
        if inspection.state is InspectionState.QUARANTINE:
            self._quarantine(claimed, source, 'structural FLAC inspection failed', now)
            return
        fingerprint = fingerprint_source(
            self._session,
            FingerprintRequest(SourceId(source.id), source_path, inspection),
            fpcalc_command=self._config.fpcalc_command,
            timeout_seconds=self._config.timeout_seconds,
        )
        tags = _read_tags(source_path, self._config.metaflac_command, self._config.timeout_seconds)
        self._capture_observations(source, source_path, tags)
        provider_result = self._lookup_providers(tags, fingerprint, now, claimed.job.kind == 'provider_retry')
        if provider_result is not None:
            self._capture_provider_evidence(source, provider_result)
        match_result = self._resolve_provider_match(source, tags, provider_result)
        metadata = _fallback_metadata(tags)
        if metadata is None:
            source.intake_state = 'needs_review'
            if not source.review_decisions:
                source.review_decisions.append(
                    ReviewDecisionRecord(state='needs_review', rationale='canonical metadata required')
                )
            record = ensure_source_record(self._session, source, now)
            record_event(
                self._session,
                record.id,
                'metadata_required',
                'needs_review',
                'canonical metadata required',
                now,
                source.id,
            )
            JobRepository(self._session).succeed(claimed, now)
            return
        if match_result is not None and match_result.decision is MatchDecision.AUTO_SELECTED:
            metadata = replace(
                metadata,
                source=CanonicalSource.VERIFIED_RELEASE,
                musicbrainz_album_id=match_result.selected_release_mbid,
            )
        staged_release = self._staging_directory(claimed.job.id)
        sanitized_path = staged_release / '.sanitized.flac'
        _ = sanitize_flac(
            FlacSanitizationRequest(
                source_path, sanitized_path, staged_release, self._config.flac_command, self._config.timeout_seconds
            )
        )
        output_path = staged_release / source_path.name
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
        sanitized_path.unlink()
        self._stage_artwork(source_path, staged_release)
        result = publish_release(
            PublicationRequest(
                staged_release,
                self._config.staging_root,
                self._config.media_root,
                (source_path,),
            )
        )
        library_record = ensure_source_record(self._session, source, now)
        metadata_revisions = record_metadata_layers(
            self._session,
            library_record.id,
            source.id,
            dict(tags),
            dict(written.tags),
            dict(written.tags),
            now,
        )
        audio_path = next(result.published_release.glob('*.flac'))
        record_publication(
            self._session,
            library_record.id,
            source.id,
            audio_path,
            _hash(audio_path),
            metadata_revisions[-1].id,
            now,
        )
        source.intake_state = 'needs_review'
        if not source.review_decisions:
            source.review_decisions.append(
                ReviewDecisionRecord(
                    state='needs_review', rationale='provider unavailable; original-tag fallback published'
                )
            )
        record_event(
            self._session,
            library_record.id,
            'publication_ready_for_review',
            'needs_review',
            'provider unavailable; original-tag fallback published',
            now,
            source.id,
        )
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

    def _quarantine(self, claimed: ClaimedJob, source: SourceRecord, reason: str, now: datetime) -> None:
        source.intake_state = 'quarantined'
        claimed.job.failure_reason = reason
        record = ensure_source_record(self._session, source, now)
        record_event(self._session, record.id, 'processing_quarantined', 'quarantined', reason, now, source.id)
        JobRepository(self._session).quarantine(claimed, now)

    def _quarantine_invalid_claim(self, claimed: ClaimedJob, reason: str, now: datetime) -> None:
        source_id = claimed.job.source_id
        source = self._session.get(SourceRecord, source_id) if source_id is not None else None
        if source is None:
            JobRepository(self._session).quarantine(claimed, now)
            return
        self._quarantine(claimed, source, reason, now)
