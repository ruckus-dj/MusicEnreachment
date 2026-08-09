from __future__ import annotations

from datetime import datetime
from typing import Protocol, final

from sqlalchemy import DateTime, ForeignKey, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship

from music_ingest.persistence.base import Base


class SourceTagView(Protocol):
    tag_name: str
    value: str
    format_name: str


class FingerprintView(Protocol):
    state: str
    fingerprint: str | None
    duration_seconds: float | None
    tool_version: str | None


class CandidateView(Protocol):
    candidate_key: str
    evidence: str


class ReviewDecisionView(Protocol):
    state: str
    rationale: str


class ProviderAttemptView(Protocol):
    provider_name: str
    outcome: str
    snapshot_sha256: str


class SourceRecordView(Protocol):
    id: str
    source_path: str
    sha256: str
    size_bytes: int
    origin: str
    intake_state: str
    library_record_id: str | None
    disappeared_at: datetime | None
    tag_observations: list[SourceTagView]
    fingerprints: list[FingerprintView]
    provider_attempts: list[ProviderAttemptView]
    candidates: list[CandidateView]
    review_decisions: list[ReviewDecisionView]


@final
class LibraryRecord(Base):
    """Stable identity for one composition across source and publication changes."""

    __tablename__ = 'library_records'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    musicbrainz_recording_id: Mapped[str | None] = mapped_column(String(36), unique=True)
    musicbrainz_release_id: Mapped[str | None] = mapped_column(String(36))
    musicbrainz_artist_id: Mapped[str | None] = mapped_column(String(36))
    source_state: Mapped[str] = mapped_column(String(32), nullable=False, default='present')
    processing_state: Mapped[str] = mapped_column(String(32), nullable=False, default='queued')
    match_state: Mapped[str] = mapped_column(String(32), nullable=False, default='unmatched')
    publication_state: Mapped[str] = mapped_column(String(32), nullable=False, default='absent')
    metadata_state: Mapped[str] = mapped_column(String(32), nullable=False, default='original')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    sources: Mapped[list[SourceRecordView]] = relationship(
        'SourceRecord', back_populates='library_record', lazy='selectin', order_by='SourceRecord.id'
    )
    publications: Mapped[list[LibraryPublicationRecord]] = relationship(
        back_populates='library_record', lazy='selectin', order_by='LibraryPublicationRecord.created_at'
    )
    metadata_revisions: Mapped[list[LibraryMetadataRevisionRecord]] = relationship(
        back_populates='library_record', lazy='selectin', order_by='LibraryMetadataRevisionRecord.created_at'
    )
    events: Mapped[list[LibraryEventRecord]] = relationship(
        back_populates='library_record', lazy='selectin', order_by='LibraryEventRecord.created_at'
    )


@final
class LibraryPublicationRecord(Base):
    """Immutable managed output version tied to the exact source version used."""

    __tablename__ = 'library_publications'

    id: Mapped[str] = mapped_column(String(96), primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    path: Mapped[str] = mapped_column(Text, nullable=False)
    format_name: Mapped[str] = mapped_column(String(32), nullable=False)
    content_sha256: Mapped[str] = mapped_column(String(64), nullable=False)
    metadata_revision_id: Mapped[int | None] = mapped_column(ForeignKey('library_metadata_revisions.id'))
    state: Mapped[str] = mapped_column(String(32), nullable=False, default='current')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(back_populates='publications')
    source: Mapped[SourceRecordView] = relationship('SourceRecord', back_populates='library_publications')
    metadata_revision: Mapped[LibraryMetadataRevisionRecord | None] = relationship(back_populates='publications')


@final
class LibraryMetadataRevisionRecord(Base):
    """Append-only metadata layer attached to a stable library record."""

    __tablename__ = 'library_metadata_revisions'
    __table_args__: tuple[UniqueConstraint, ...] = (UniqueConstraint('library_record_id', 'layer', 'revision'),)

    id: Mapped[int] = mapped_column(primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    layer: Mapped[str] = mapped_column(String(16), nullable=False)
    revision: Mapped[int] = mapped_column(nullable=False)
    tags_json: Mapped[str] = mapped_column(Text, nullable=False)
    actor: Mapped[str] = mapped_column(String(128), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(back_populates='metadata_revisions')
    publications: Mapped[list[LibraryPublicationRecord]] = relationship(back_populates='metadata_revision')


@final
class LibraryEventRecord(Base):
    """Append-only processing or identity event explaining a library state."""

    __tablename__ = 'library_events'

    id: Mapped[int] = mapped_column(primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    kind: Mapped[str] = mapped_column(String(64), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    reason: Mapped[str | None] = mapped_column(Text)
    details_json: Mapped[str] = mapped_column(Text, nullable=False, default='{}')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(back_populates='events')
