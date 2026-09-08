from __future__ import annotations

from datetime import UTC, datetime
from typing import final

from sqlalchemy import BigInteger, DateTime, Float, ForeignKey, Index, Integer, LargeBinary, Text, select
from sqlalchemy.orm import Mapped, Session, mapped_column, relationship

from music_ingest.models.db import Base
from music_ingest.models.library import (
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
)
from music_ingest.models.workflow import (
    JobAttemptRecord,
    JobRecord,
    WebhookReceiptRecord,
)


@final
class SourceRecord(Base):
    __tablename__ = 'source_records'
    __table_args__ = (Index('ix_source_records_library_record_id', 'library_record_id'),)

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    source_path: Mapped[str] = mapped_column(Text, nullable=False)
    device: Mapped[int] = mapped_column(BigInteger, nullable=False)
    inode: Mapped[int] = mapped_column(BigInteger, nullable=False)
    size_bytes: Mapped[int] = mapped_column(BigInteger, nullable=False)
    sha256: Mapped[str] = mapped_column(Text, nullable=False)
    mtime_ns: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0, server_default='0')
    duration_seconds: Mapped[int | None] = mapped_column(Integer)
    origin: Mapped[str] = mapped_column(Text, nullable=False)
    intake_state: Mapped[str] = mapped_column(Text, nullable=False)
    source_root_id: Mapped[str] = mapped_column(ForeignKey('source_roots.id'), nullable=False, server_default='legacy')
    library_record_id: Mapped[str | None] = mapped_column(ForeignKey('library_records.id'))
    replaced_by_source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    disappeared_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    media_codec: Mapped[str | None] = mapped_column(Text)
    media_bit_depth: Mapped[int | None] = mapped_column(Integer)
    media_sample_rate: Mapped[int | None] = mapped_column(Integer)
    media_channels: Mapped[int | None] = mapped_column(Integer)
    media_bitrate: Mapped[int | None] = mapped_column(Integer)

    tag_observations: Mapped[list[SourceTagRecord]] = relationship(back_populates='source', lazy='selectin')
    artwork_observations: Mapped[list[ArtworkRecord]] = relationship(back_populates='source', lazy='selectin')
    provider_attempts: Mapped[list[ProviderAttemptRecord]] = relationship(back_populates='source', lazy='selectin')
    candidate_runs: Mapped[list[ProviderCandidateRunRecord]] = relationship(back_populates='source', lazy='selectin')
    candidates: Mapped[list[CandidateRecord]] = relationship(back_populates='source', lazy='selectin')
    review_decisions: Mapped[list[ReviewDecisionRecord]] = relationship(back_populates='source', lazy='selectin')
    fingerprints: Mapped[list[FingerprintRecord]] = relationship(
        back_populates='source', lazy='selectin', order_by='FingerprintRecord.id'
    )
    decoder_evidence: Mapped[list[DecoderEvidenceRecord]] = relationship(back_populates='source', lazy='selectin')
    library_record: Mapped[LibraryRecord | None] = relationship(back_populates='sources')
    library_publications: Mapped[list[LibraryPublicationRecord]] = relationship(
        'LibraryPublicationRecord', back_populates='source', lazy='selectin'
    )
    source_root: Mapped[SourceRootRecord] = relationship(back_populates='sources')
    recording_assignments: Mapped[list[SourceRecordingAssignmentRecord]] = relationship(
        back_populates='source', lazy='selectin', order_by='SourceRecordingAssignmentRecord.created_at'
    )
    association_override: Mapped[SourceAssociationOverrideRecord | None] = relationship(back_populates='source')

    @staticmethod
    def get(session: Session, source_id: str) -> SourceRecord | None:
        return session.get(SourceRecord, source_id)

    @staticmethod
    def get_by_path(session: Session, source_path: str) -> SourceRecord | None:
        return session.scalar(select(SourceRecord).where(SourceRecord.source_path == source_path))


@final
class SourceRootRecord(Base):
    """A mutable configured root that owns immutable source observations."""

    __tablename__ = 'source_roots'

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    display_name: Mapped[str] = mapped_column(Text, nullable=False)
    canonical_path: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    enabled: Mapped[bool] = mapped_column(nullable=False, default=True)
    scan_state: Mapped[str] = mapped_column(Text, nullable=False, default='never_scanned')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    sources: Mapped[list[SourceRecord]] = relationship(back_populates='source_root', lazy='selectin')


@final
class SourceRecordingAssignmentRecord(Base):
    """Append-only evidence of a source's recording aggregate assignment."""

    __tablename__ = 'source_recording_assignments'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    library_record_id: Mapped[str | None] = mapped_column(ForeignKey('library_records.id'))
    state: Mapped[str] = mapped_column(Text, nullable=False)
    actor: Mapped[str] = mapped_column(Text, nullable=False)
    rationale: Mapped[str | None] = mapped_column(Text)
    evidence_json: Mapped[str] = mapped_column(Text, nullable=False, default='{}')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    source: Mapped[SourceRecord] = relationship(back_populates='recording_assignments')


@final
class SourceAssociationOverrideRecord(Base):
    """Current operator override metadata without mutating assignment history."""

    __tablename__ = 'source_association_overrides'

    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), primary_key=True)
    recording_mbid: Mapped[str] = mapped_column(Text, nullable=False)
    actor: Mapped[str] = mapped_column(Text, nullable=False)
    rationale: Mapped[str | None] = mapped_column(Text)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    cleared_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))

    source: Mapped[SourceRecord] = relationship(back_populates='association_override')


@final
class SourceTagRecord(Base):
    __tablename__ = 'source_tag_observations'
    __table_args__ = (Index('ix_source_tag_observations_tag_value_source', 'tag_name', 'value', 'source_id'),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    format_name: Mapped[str] = mapped_column(Text, nullable=False)
    tag_name: Mapped[str] = mapped_column(Text, nullable=False)
    value: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='tag_observations')


@final
class ArtworkRecord(Base):
    __tablename__ = 'artwork_hash_observations'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    sha256: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='artwork_observations')


@final
class ProviderAttemptRecord(Base):
    __tablename__ = 'provider_attempts'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    provider_name: Mapped[str] = mapped_column(Text, nullable=False)
    outcome: Mapped[str] = mapped_column(Text, nullable=False)
    snapshot_sha256: Mapped[str] = mapped_column(Text, nullable=False)
    snapshot: Mapped[str] = mapped_column(Text, nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), nullable=False, default=lambda: datetime.now(UTC)
    )
    source: Mapped[SourceRecord] = relationship(back_populates='provider_attempts')


@final
class ProviderCandidateRunRecord(Base):
    __tablename__ = 'provider_candidate_runs'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    provider_name: Mapped[str] = mapped_column(Text, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='candidate_runs')
    candidates: Mapped[list[CandidateRecord]] = relationship(back_populates='run', lazy='selectin')


@final
class CandidateRecord(Base):
    __tablename__ = 'candidate_evidence'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    run_id: Mapped[int | None] = mapped_column(ForeignKey('provider_candidate_runs.id'))
    candidate_key: Mapped[str] = mapped_column(Text, nullable=False)
    evidence: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='candidates')
    run: Mapped[ProviderCandidateRunRecord | None] = relationship(back_populates='candidates')


@final
class ReviewDecisionRecord(Base):
    __tablename__ = 'review_decisions'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    rationale: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='review_decisions')


@final
class FingerprintRecord(Base):
    __tablename__ = 'fingerprint_evidence'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    fingerprint: Mapped[str | None] = mapped_column(Text)
    duration_seconds: Mapped[float | None] = mapped_column(Float)
    tool_version: Mapped[str | None] = mapped_column(Text)
    output_sha256: Mapped[str] = mapped_column(Text, nullable=False)
    tool_state: Mapped[str | None] = mapped_column(Text)
    return_code: Mapped[int | None] = mapped_column(Integer)
    version_tool_state: Mapped[str | None] = mapped_column(Text)
    version_return_code: Mapped[int | None] = mapped_column(Integer)
    version_output_sha256: Mapped[str | None] = mapped_column(Text)
    source: Mapped[SourceRecord] = relationship(back_populates='fingerprints')


@final
class DecoderEvidenceRecord(Base):
    __tablename__ = 'decoder_evidence'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    decoder_command: Mapped[str] = mapped_column(Text, nullable=False)
    tool_state: Mapped[str] = mapped_column(Text, nullable=False)
    return_code: Mapped[int | None] = mapped_column(Integer)
    output_sha256: Mapped[str] = mapped_column(Text, nullable=False)
    checked_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='decoder_evidence')


@final
class ProviderSnapshotRecord(Base):
    __tablename__ = 'provider_snapshots'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    provider_name: Mapped[str] = mapped_column(Text, nullable=False)
    request_hash: Mapped[str] = mapped_column(Text, nullable=False)
    request_descriptor: Mapped[str] = mapped_column(Text, nullable=False)
    response_sha256: Mapped[str] = mapped_column(Text, nullable=False)
    response_body: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    captured_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    outcome: Mapped[str] = mapped_column(Text, nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    age_seconds: Mapped[int | None] = mapped_column(Integer)
    http_status: Mapped[int | None] = mapped_column(Integer)


@final
class ProviderScheduleRecord(Base):
    __tablename__ = 'provider_schedules'

    provider_name: Mapped[str] = mapped_column(Text, primary_key=True)
    next_start_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    lease_until: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    lease_token: Mapped[str | None] = mapped_column(Text)


@final
class RuntimeSettingRecord(Base):
    __tablename__ = 'runtime_settings'

    key: Mapped[str] = mapped_column(Text, primary_key=True)
    value: Mapped[str] = mapped_column(Text, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


@final
class StorageConfigRecord(Base):
    __tablename__ = 'storage_config'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    output_root: Mapped[str] = mapped_column(Text, nullable=False)
    migration_json: Mapped[str | None] = mapped_column(Text)
    state: Mapped[str] = mapped_column(Text, nullable=False, default='ready')
    generation: Mapped[int] = mapped_column(Integer, nullable=False, default=1)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


@final
class UnsortedFilenameCounterRecord(Base):
    __tablename__ = 'unsorted_filename_counters'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    next_number: Mapped[int] = mapped_column(Integer, nullable=False)


@final
class GenreCatalogRecord(Base):
    __tablename__ = 'genre_catalog'

    musicbrainz_id: Mapped[str] = mapped_column(Text, primary_key=True)
    source_name: Mapped[str] = mapped_column(Text, unique=True, nullable=False)
    display_name: Mapped[str] = mapped_column(Text, nullable=False)
    normalized_key: Mapped[str] = mapped_column(Text, index=True, nullable=False)
    synced_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    @staticmethod
    def list_all(session: Session) -> tuple[GenreCatalogRecord, ...]:
        return tuple(session.scalars(select(GenreCatalogRecord).order_by(GenreCatalogRecord.display_name)).all())


__all__ = [
    'ArtworkRecord',
    'Base',
    'CandidateRecord',
    'FingerprintRecord',
    'LibraryEventRecord',
    'LibraryMetadataRevisionRecord',
    'LibraryPublicationRecord',
    'LibraryRecord',
    'JobAttemptRecord',
    'JobRecord',
    'ProviderAttemptRecord',
    'ProviderScheduleRecord',
    'RuntimeSettingRecord',
    'GenreCatalogRecord',
    'ProviderSnapshotRecord',
    'ReviewDecisionRecord',
    'SourceRecord',
    'SourceAssociationOverrideRecord',
    'SourceRecordingAssignmentRecord',
    'SourceRootRecord',
    'SourceTagRecord',
    'StorageConfigRecord',
    'UnsortedFilenameCounterRecord',
    'WebhookReceiptRecord',
]
