from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from hashlib import sha256
from pathlib import Path

from sqlalchemy.orm import Session

from music_ingest.enrichment.fingerprints import (
    FingerprintRequest,
    FingerprintResult,
    FingerprintState,
    fingerprint_source,
)
from music_ingest.inspectors._tool import ToolEvidence
from music_ingest.intake.service import SourceId
from music_ingest.models import (
    ArtworkRecord,
    SourceRecord,
)
from music_ingest.models.entities import DecoderEvidenceRecord
from music_ingest.models.repositories import DecoderEvidenceRepository, FingerprintRepository
from music_ingest.normalize.source_evidence import read_source_fields
from music_ingest.processing.config import ProcessingConfig
from music_ingest.processing.metadata import (
    file_hash,
)
from music_ingest.processing.support.settings import RuntimeProcessingSettings


@dataclass(frozen=True, slots=True)
class SourceEvidence:
    session: Session
    config: ProcessingConfig
    settings: RuntimeProcessingSettings

    def cached_fingerprint(self, source: SourceRecord) -> FingerprintResult | None:
        fingerprint = FingerprintRepository(self.session).successful_evidence(source.id)
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

    def analyze_source(self, source: SourceRecord, source_path: Path) -> FingerprintResult | None:
        """Provider processing uses persisted evidence, including explicit absence."""
        return self.cached_fingerprint(source)

    def import_fingerprint(self, source: SourceRecord, source_path: Path) -> FingerprintResult | None:
        cached_fingerprint = self.cached_fingerprint(source)
        if cached_fingerprint is not None:
            return cached_fingerprint
        return fingerprint_source(
            self.session,
            FingerprintRequest(SourceId(source.id), source_path, None),
            fpcalc_command=self.config.fpcalc_command,
            timeout_seconds=self.settings.timeout_seconds(),
        )

    def record_decoder_evidence(self, source: SourceRecord, evidence: ToolEvidence, now: datetime) -> None:
        _ = DecoderEvidenceRepository(self.session).add_evidence(
            DecoderEvidenceRecord(
                source_id=source.id,
                decoder_command=self.config.ffmpeg_command,
                tool_state=evidence.state.value,
                return_code=evidence.return_code,
                output_sha256=sha256(evidence.stdout.encode()).hexdigest(),
                checked_at=now,
            )
        )

    def capture_observations(self, source: SourceRecord, path: Path, tags: tuple[tuple[str, str], ...]) -> None:
        if source.tag_observations:
            return
        source.tag_observations.extend(read_source_fields(path, tags))
        for artwork in (path.parent / 'cover.jpg', path.parent / 'cover.webp'):
            if artwork.is_file():
                source.artwork_observations.append(ArtworkRecord(sha256=file_hash(artwork)))
