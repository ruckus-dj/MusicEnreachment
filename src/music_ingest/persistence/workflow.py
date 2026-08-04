from __future__ import annotations

from datetime import datetime
from typing import TYPE_CHECKING, final

from sqlalchemy import CheckConstraint, DateTime, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship

from music_ingest.persistence.base import Base

if TYPE_CHECKING:
    from music_ingest.persistence.models import SourceRecord


@final
class WebhookReceiptRecord(Base):
    __tablename__ = 'webhook_receipts'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    event_fingerprint: Mapped[str] = mapped_column(String(64), unique=True, nullable=False)
    provider_name: Mapped[str] = mapped_column(String(64), nullable=False)
    payload_json: Mapped[str] = mapped_column(Text, nullable=False)
    received_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    job_id: Mapped[str | None] = mapped_column(ForeignKey('jobs.id'))
    job: Mapped[JobRecord | None] = relationship(back_populates='webhook_receipts')


@final
class ReleaseGroupRecord(Base):
    __tablename__ = 'release_groups'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    title: Mapped[str] = mapped_column(Text, nullable=False)
    releases: Mapped[list[ReleaseRecord]] = relationship(back_populates='release_group', lazy='selectin')


@final
class ReleaseRecord(Base):
    __tablename__ = 'releases'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    release_group_id: Mapped[str] = mapped_column(ForeignKey('release_groups.id'), nullable=False)
    title: Mapped[str] = mapped_column(Text, nullable=False)
    release_group: Mapped[ReleaseGroupRecord] = relationship(back_populates='releases')
    tracks: Mapped[list[TrackRecord]] = relationship(
        back_populates='release', lazy='selectin', order_by='TrackRecord.position'
    )
    publication: Mapped[PublicationStateRecord | None] = relationship(
        back_populates='release', lazy='selectin', uselist=False
    )
    audits: Mapped[list[AuditRecord]] = relationship(
        back_populates='release', lazy='selectin', order_by='AuditRecord.id'
    )


@final
class TrackRecord(Base):
    __tablename__ = 'tracks'
    __table_args__ = (UniqueConstraint('release_id', 'position', name='uq_track_release_position'),)

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    release_id: Mapped[str] = mapped_column(ForeignKey('releases.id'), nullable=False)
    position: Mapped[int] = mapped_column(Integer, nullable=False)
    title: Mapped[str] = mapped_column(Text, nullable=False)
    release: Mapped[ReleaseRecord] = relationship(back_populates='tracks')
    files: Mapped[list[ReleaseFileRecord]] = relationship(
        back_populates='track', lazy='selectin', order_by='ReleaseFileRecord.relative_path'
    )


@final
class ReleaseFileRecord(Base):
    __tablename__ = 'release_files'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    track_id: Mapped[str] = mapped_column(ForeignKey('tracks.id'), nullable=False)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    relative_path: Mapped[str] = mapped_column(Text, unique=True, nullable=False)
    content_sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    track: Mapped[TrackRecord] = relationship(back_populates='files')
    source: Mapped[SourceRecord | None] = relationship(back_populates='release_files')
    tag_layers: Mapped[list[TagLayerRecord]] = relationship(
        back_populates='release_file', lazy='selectin', order_by='TagLayerRecord.layer, TagLayerRecord.revision'
    )
    tombstone: Mapped[TombstoneRecord | None] = relationship(
        back_populates='release_file', lazy='selectin', uselist=False
    )


@final
class TagLayerRecord(Base):
    __tablename__ = 'tag_layers'
    __table_args__ = (
        CheckConstraint('revision > 0', name='ck_tag_layer_revision_positive'),
        UniqueConstraint('release_file_id', 'layer', 'revision', name='uq_tag_layer_revision'),
    )

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    release_file_id: Mapped[str] = mapped_column(ForeignKey('release_files.id'), nullable=False)
    layer: Mapped[str] = mapped_column(String(16), nullable=False)
    revision: Mapped[int] = mapped_column(Integer, nullable=False)
    tags_json: Mapped[str] = mapped_column(Text, nullable=False)
    recorded_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    release_file: Mapped[ReleaseFileRecord] = relationship(back_populates='tag_layers')


@final
class JobRecord(Base):
    __tablename__ = 'jobs'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    kind: Mapped[str] = mapped_column(String(64), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    next_attempt_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    failure_reason: Mapped[str | None] = mapped_column(Text)
    attempts: Mapped[list[JobAttemptRecord]] = relationship(
        back_populates='job', lazy='selectin', order_by='JobAttemptRecord.attempt_number'
    )
    webhook_receipts: Mapped[list[WebhookReceiptRecord]] = relationship(back_populates='job', lazy='selectin')


@final
class JobAttemptRecord(Base):
    __tablename__ = 'job_attempts'
    __table_args__ = (UniqueConstraint('job_id', 'attempt_number', name='uq_job_attempt_number'),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    job_id: Mapped[str] = mapped_column(ForeignKey('jobs.id'), nullable=False)
    attempt_number: Mapped[int] = mapped_column(Integer, nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    started_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    job: Mapped[JobRecord] = relationship(back_populates='attempts')


@final
class PublicationStateRecord(Base):
    __tablename__ = 'publication_states'

    release_id: Mapped[str] = mapped_column(ForeignKey('releases.id'), primary_key=True)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    release: Mapped[ReleaseRecord] = relationship(back_populates='publication')


@final
class TombstoneRecord(Base):
    __tablename__ = 'tombstones'

    release_file_id: Mapped[str] = mapped_column(ForeignKey('release_files.id'), primary_key=True)
    reason: Mapped[str] = mapped_column(Text, nullable=False)
    recorded_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    release_file: Mapped[ReleaseFileRecord] = relationship(back_populates='tombstone')


@final
class AuditRecord(Base):
    __tablename__ = 'audit_records'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    release_id: Mapped[str] = mapped_column(ForeignKey('releases.id'), nullable=False)
    action: Mapped[str] = mapped_column(String(64), nullable=False)
    actor: Mapped[str] = mapped_column(String(128), nullable=False)
    details_json: Mapped[str] = mapped_column(Text, nullable=False)
    recorded_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    release: Mapped[ReleaseRecord] = relationship(back_populates='audits')
