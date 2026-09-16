from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.enrichment.fingerprints import (
    FingerprintResult,
)
from music_ingest.library.service import (
    ensure_source_record,
    record_event,
)
from music_ingest.matching.evidence import ProviderEvidenceRequest, ProviderEvidenceResult, ProviderEvidenceService
from music_ingest.matching.providers import (
    AcoustIdMatch,
    AcoustIdResult,
    Ambiguous,
    Disabled,
    FixtureCase,
    FixtureProvenance,
    LiveProvenance,
    Malformed,
    MusicBrainzMatch,
    MusicBrainzResult,
    NoMatch,
    RateLimited,
    RecordingCandidate,
    Timeout,
    Unavailable,
)
from music_ingest.matching.scoring import (
    MatchingRequest,
    MatchResult,
)
from music_ingest.models import (
    CandidateRecord,
    JobRecord,
    ProviderAttemptRecord,
    ProviderCandidateRunRecord,
    SourceRecord,
)
from music_ingest.models.jobs import ClaimedJob, JobRepository
from music_ingest.normalize.source_values import source_values
from music_ingest.processing.candidates import (
    _acoustid_recording_mbids,
    _candidate_records,
    _matching_request,
    _tag_number,
    musicbrainz_lookup_ids,
)
from music_ingest.processing.execution import (
    ChangedSource,
    ExecutionContext,
    HandlerOutcome,
    QuarantineSource,
)
from music_ingest.processing.support.evidence import SourceEvidence
from music_ingest.processing.support.settings import RuntimeProcessingSettings
from music_ingest.processing.support.sources import SourceAccess


@dataclass(frozen=True, slots=True)
class AnalysisHandler:
    session: Session
    sources: SourceAccess
    evidence: SourceEvidence
    settings: RuntimeProcessingSettings

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome:
        now = context.now
        source = self.sources.locked_source(claimed)
        source_path = self.sources.owned_source_path(source)
        if isinstance(source_path, QuarantineSource):
            return source_path
        if self.sources.changed(source, source_path):
            return ChangedSource(source.id, source_path)
        fingerprint = self.evidence.analyze_source(source, source_path)
        if fingerprint is None:
            record = ensure_source_record(self.session, source, now)
            record_event(
                self.session,
                record.id,
                'source_evidence_unavailable',
                'needs_review',
                'stored fingerprint unavailable; explicit reimport is required',
                now,
                source.id,
            )
            return
        tags = tuple(source_values(source.tag_observations).items())
        record = ensure_source_record(self.session, source, now)
        recording_mbid, release_mbid = musicbrainz_lookup_ids(record, source)
        recording_mbids = tuple(
            dict.fromkeys((*_acoustid_recording_mbids(source), *((recording_mbid,) if recording_mbid else ())))
        )
        provider_result = self.lookup_providers(
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
                self.session,
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
                _ = self.capture_provider_attempt(source, 'acoustid', acoustic_result, None, now)
            configured_musicbrainz, _, _ = self.settings.configured_providers()
            if configured_musicbrainz is not None:
                _ = JobRepository(self.session).enqueue(source.id, 'musicbrainz_analysis', now)
                record_event(
                    self.session,
                    record.id,
                    'acoustid_analysis_ready',
                    'analyzing',
                    'AcousticID analysis is complete; MusicBrainz analysis queued',
                    now,
                    source.id,
                )
            else:
                self.enqueue_candidate_selection_if_ready(source, claimed.job.id, now)
                record_event(
                    self.session,
                    record.id,
                    'acoustid_analysis_ready',
                    'needs_review',
                    'AcousticID analysis is complete; MusicBrainz is not configured',
                    now,
                    source.id,
                )
            return
        candidate_request = _matching_request(record, source, tags)
        _ = self.capture_provider_attempt(
            source,
            'musicbrainz',
            provider_result.musicbrainz,
            None,
            now,
            candidate_request,
        )
        self.enqueue_candidate_selection_if_ready(source, claimed.job.id, now)

    def lookup_providers(
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
        configured_musicbrainz, configured_acoustid, _ = self.settings.configured_providers()
        musicbrainz = configured_musicbrainz if run_musicbrainz and (query or recording_mbid or release_mbid) else None
        acoustid = (
            configured_acoustid
            if run_acoustid and fingerprint.fingerprint is not None and fingerprint.duration_seconds is not None
            else None
        )
        if musicbrainz is None and acoustid is None:
            return None
        return ProviderEvidenceService(self.session, musicbrainz, acoustid, commit_on_persist=False).lookup(
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
                acoustid_confidence_threshold=self.settings.confidence_threshold(),
                artist_name=values.get('ARTIST'),
                recording_mbid=recording_mbid,
                release_mbid=release_mbid,
                recording_mbids=recording_mbids,
                run_acoustid=run_acoustid,
                run_musicbrainz=run_musicbrainz,
            ),
            now,
        )

    def capture_provider_attempt(
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
                    source_metadata_revision=source.source_metadata_revision,
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

    def enqueue_candidate_selection_if_ready(self, source: SourceRecord, current_job_id: str, now: datetime) -> None:
        active_collection = self.session.scalar(
            select(JobRecord)
            .where(JobRecord.source_id == source.id)
            .where(JobRecord.kind.in_(['filesystem_scan', 'acoustid_analysis', 'musicbrainz_analysis']))
            .where(JobRecord.state.in_(['queued', 'running']))
            .where(JobRecord.id != current_job_id)
        )
        if active_collection is not None:
            return
        musicbrainz, acoustid, _ = self.settings.configured_providers()
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
        _ = JobRepository(self.session).enqueue(source.id, 'candidate_selection', now)
