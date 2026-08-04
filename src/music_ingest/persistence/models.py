from __future__ import annotations

from datetime import datetime
from typing import final

from sqlalchemy import BigInteger, DateTime, Float, ForeignKey, Integer, LargeBinary, String, Text
from sqlalchemy.orm import Mapped, mapped_column, relationship

from music_ingest.persistence.base import Base
from music_ingest.persistence.workflow import (
    AuditRecord,
    JobAttemptRecord,
    JobRecord,
    PublicationStateRecord,
    ReleaseFileRecord,
    ReleaseGroupRecord,
    ReleaseRecord,
    TagLayerRecord,
    TombstoneRecord,
    TrackRecord,
    WebhookReceiptRecord,
)


@final
class SourceRecord(Base):
    __tablename__ = 'source_records'

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    source_path: Mapped[str] = mapped_column(Text, nullable=False)
    device: Mapped[int] = mapped_column(BigInteger, nullable=False)
    inode: Mapped[int] = mapped_column(BigInteger, nullable=False)
    size_bytes: Mapped[int] = mapped_column(BigInteger, nullable=False)
    sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    duration_seconds: Mapped[int | None] = mapped_column(Integer)
    origin: Mapped[str] = mapped_column(String(16), nullable=False)
    intake_state: Mapped[str] = mapped_column(String(32), nullable=False)

    tag_observations: Mapped[list[SourceTagRecord]] = relationship(back_populates='source', lazy='selectin')
    artwork_observations: Mapped[list[ArtworkRecord]] = relationship(back_populates='source', lazy='selectin')
    provider_attempts: Mapped[list[ProviderAttemptRecord]] = relationship(back_populates='source', lazy='selectin')
    candidates: Mapped[list[CandidateRecord]] = relationship(back_populates='source', lazy='selectin')
    review_decisions: Mapped[list[ReviewDecisionRecord]] = relationship(back_populates='source', lazy='selectin')
    fingerprints: Mapped[list[FingerprintRecord]] = relationship(back_populates='source', lazy='selectin')
    publication: Mapped[PublicationRecord | None] = relationship(
        back_populates='source', lazy='selectin', uselist=False
    )
    review_release: Mapped[ReviewReleaseRecord | None] = relationship(
        back_populates='source', lazy='selectin', uselist=False
    )
    review_audits: Mapped[list[ReviewAuditRecord]] = relationship(back_populates='source', lazy='selectin')
    publish_snapshots: Mapped[list[PublishSnapshotRecord]] = relationship(back_populates='source', lazy='selectin')
    release_files: Mapped[list[ReleaseFileRecord]] = relationship(back_populates='source', lazy='selectin')


@final
class SourceTagRecord(Base):
    __tablename__ = 'source_tag_observations'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    format_name: Mapped[str] = mapped_column(String(32), nullable=False)
    tag_name: Mapped[str] = mapped_column(String(128), nullable=False)
    value: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='tag_observations')


@final
class ArtworkRecord(Base):
    __tablename__ = 'artwork_hash_observations'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='artwork_observations')


@final
class ProviderAttemptRecord(Base):
    __tablename__ = 'provider_attempts'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    provider_name: Mapped[str] = mapped_column(String(64), nullable=False)
    outcome: Mapped[str] = mapped_column(String(32), nullable=False)
    snapshot_sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    snapshot: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='provider_attempts')


@final
class CandidateRecord(Base):
    __tablename__ = 'candidate_evidence'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    candidate_key: Mapped[str] = mapped_column(String(255), nullable=False)
    evidence: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='candidates')


@final
class ReviewDecisionRecord(Base):
    __tablename__ = 'review_decisions'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    rationale: Mapped[str] = mapped_column(Text, nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='review_decisions')


@final
class FingerprintRecord(Base):
    __tablename__ = 'fingerprint_evidence'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    fingerprint: Mapped[str | None] = mapped_column(Text)
    duration_seconds: Mapped[float | None] = mapped_column(Float)
    tool_version: Mapped[str | None] = mapped_column(String(64))
    output_sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    tool_state: Mapped[str | None] = mapped_column(String(32))
    return_code: Mapped[int | None] = mapped_column(Integer)
    version_tool_state: Mapped[str | None] = mapped_column(String(32))
    version_return_code: Mapped[int | None] = mapped_column(Integer)
    version_output_sha256: Mapped[str | None] = mapped_column(String(64))
    source: Mapped[SourceRecord] = relationship(back_populates='fingerprints')


@final
class PublicationRecord(Base):
    __tablename__ = 'publication_records'

    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), primary_key=True)
    publication_state: Mapped[str] = mapped_column(String(32), nullable=False)
    published_path: Mapped[str | None] = mapped_column(Text)
    source: Mapped[SourceRecord] = relationship(back_populates='publication')


@final
class ReviewReleaseRecord(Base):
    __tablename__ = 'review_releases'

    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), primary_key=True)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    original_json: Mapped[str] = mapped_column(Text, nullable=False)
    proposed_json: Mapped[str] = mapped_column(Text, nullable=False)
    artist_id: Mapped[str] = mapped_column(String(96), nullable=False)
    release_id: Mapped[str] = mapped_column(String(96), nullable=False)
    track_id: Mapped[str] = mapped_column(String(96), nullable=False)
    musicbrainz_id: Mapped[str | None] = mapped_column(String(36))
    source: Mapped[SourceRecord] = relationship(back_populates='review_release')


@final
class ReviewAuditRecord(Base):
    __tablename__ = 'review_audits'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    action: Mapped[str] = mapped_column(String(32), nullable=False)
    before_json: Mapped[str] = mapped_column(Text, nullable=False)
    after_json: Mapped[str] = mapped_column(Text, nullable=False)
    actor: Mapped[str] = mapped_column(String(128), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='review_audits')


@final
class PublishSnapshotRecord(Base):
    __tablename__ = 'publish_snapshots'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    release_json: Mapped[str] = mapped_column(Text, nullable=False)
    captured_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    source: Mapped[SourceRecord] = relationship(back_populates='publish_snapshots')


@final
class ProviderSnapshotRecord(Base):
    __tablename__ = 'provider_snapshots'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    provider_name: Mapped[str] = mapped_column(String(64), nullable=False)
    request_hash: Mapped[str] = mapped_column(String(64), nullable=False)
    request_descriptor: Mapped[str] = mapped_column(Text, nullable=False)
    response_sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    response_body: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    captured_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    outcome: Mapped[str] = mapped_column(String(32), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    age_seconds: Mapped[int | None] = mapped_column(Integer)
    http_status: Mapped[int | None] = mapped_column(Integer)


@final
class ProviderScheduleRecord(Base):
    __tablename__ = 'provider_schedules'

    provider_name: Mapped[str] = mapped_column(String(64), primary_key=True)
    next_start_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    lease_until: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    lease_token: Mapped[str | None] = mapped_column(String(64))


@final
class RuntimeSettingRecord(Base):
    __tablename__ = 'runtime_settings'

    key: Mapped[str] = mapped_column(String(64), primary_key=True)
    value: Mapped[str] = mapped_column(String(255), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


__all__ = [
    'ArtworkRecord',
    'AuditRecord',
    'Base',
    'CandidateRecord',
    'FingerprintRecord',
    'JobAttemptRecord',
    'JobRecord',
    'ProviderAttemptRecord',
    'ProviderScheduleRecord',
    'RuntimeSettingRecord',
    'ProviderSnapshotRecord',
    'PublicationRecord',
    'PublicationStateRecord',
    'PublishSnapshotRecord',
    'ReleaseFileRecord',
    'ReleaseGroupRecord',
    'ReleaseRecord',
    'ReviewAuditRecord',
    'ReviewDecisionRecord',
    'ReviewReleaseRecord',
    'SourceRecord',
    'SourceTagRecord',
    'TagLayerRecord',
    'TombstoneRecord',
    'TrackRecord',
    'WebhookReceiptRecord',
]
