from __future__ import annotations

from datetime import datetime
from typing import Protocol, final

from sqlalchemy import CheckConstraint, DateTime, ForeignKey, Index, Text, UniqueConstraint, select, text
from sqlalchemy.orm import Mapped, Session, mapped_column, relationship

from music_ingest.models.db import Base


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
    run_id: int | None
    candidate_key: str
    evidence: str


class ReviewDecisionView(Protocol):
    state: str
    rationale: str


class ProviderAttemptView(Protocol):
    provider_name: str
    outcome: str
    snapshot_sha256: str
    created_at: datetime


class ProviderCandidateRunView(Protocol):
    id: int
    provider_name: str


class SourceRecordView(Protocol):
    id: str
    source_path: str
    sha256: str
    size_bytes: int
    origin: str
    intake_state: str
    source_root_id: str
    library_record_id: str | None
    replaced_by_source_id: str | None
    disappeared_at: datetime | None
    media_codec: str | None
    media_bit_depth: int | None
    media_sample_rate: int | None
    media_channels: int | None
    media_bitrate: int | None
    tag_observations: list[SourceTagView]
    fingerprints: list[FingerprintView]
    provider_attempts: list[ProviderAttemptView]
    candidate_runs: list[ProviderCandidateRunView]
    candidates: list[CandidateView]
    review_decisions: list[ReviewDecisionView]


@final
class LibraryRecord(Base):
    """Stable identity for one composition across source and publication changes."""

    __tablename__ = 'library_records'
    __table_args__: tuple[UniqueConstraint | Index | CheckConstraint, ...] = (
        CheckConstraint(
            "lyrics_status IN ('none', 'pending', 'synced', 'no_candidate', 'validation_rejected', 'error')",
            name='ck_library_records_lyrics_status',
        ),
        UniqueConstraint('musicbrainz_recording_id', 'musicbrainz_release_id'),
        Index(
            'uq_library_records_musicbrainz_recording_without_release',
            'musicbrainz_recording_id',
            unique=True,
            postgresql_where=text('musicbrainz_recording_id IS NOT NULL AND musicbrainz_release_id IS NULL'),
        ).ddl_if(dialect='postgresql'),
        Index('ix_library_records_release_publication', 'musicbrainz_release_id', 'publication_state'),
    )

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    musicbrainz_recording_id: Mapped[str | None] = mapped_column(Text)
    musicbrainz_release_id: Mapped[str | None] = mapped_column(Text)
    musicbrainz_artist_id: Mapped[str | None] = mapped_column(Text)
    source_state: Mapped[str] = mapped_column(Text, nullable=False, default='present')
    processing_state: Mapped[str] = mapped_column(Text, nullable=False, default='queued')
    match_state: Mapped[str] = mapped_column(Text, nullable=False, default='unmatched')
    publication_state: Mapped[str] = mapped_column(Text, nullable=False, default='absent')
    metadata_state: Mapped[str] = mapped_column(Text, nullable=False, default='original')
    lyrics_status: Mapped[str] = mapped_column(Text, nullable=False, default='none', server_default=text("'none'"))
    lyrics_path: Mapped[str | None] = mapped_column(Text)
    lyrics_publication_id: Mapped[str | None] = mapped_column(ForeignKey('library_publications.id'))
    lyrics_sha256: Mapped[str | None] = mapped_column(Text)
    lyrics_updated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    # Reusable evidence survives publication rebinding; lyric text remains on disk only.
    lyrics_evidence_json: Mapped[str | None] = mapped_column(Text)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    sources: Mapped[list[SourceRecordView]] = relationship(
        'SourceRecord', back_populates='library_record', lazy='selectin', order_by='SourceRecord.id'
    )
    publications: Mapped[list[LibraryPublicationRecord]] = relationship(
        back_populates='library_record',
        foreign_keys='LibraryPublicationRecord.library_record_id',
        lazy='selectin',
        order_by='LibraryPublicationRecord.created_at',
    )
    metadata_revisions: Mapped[list[LibraryMetadataRevisionRecord]] = relationship(
        back_populates='library_record', lazy='selectin', order_by='LibraryMetadataRevisionRecord.created_at'
    )
    events: Mapped[list[LibraryEventRecord]] = relationship(
        back_populates='library_record', lazy='selectin', order_by='LibraryEventRecord.created_at'
    )
    effective_source_decision: Mapped[EffectiveSourceDecisionRecord | None] = relationship(
        back_populates='library_record', cascade='all, delete-orphan'
    )
    publication_attempts: Mapped[list[PublicationAttemptRecord]] = relationship(back_populates='library_record')

    @staticmethod
    def get(session: Session, record_id: str) -> LibraryRecord | None:
        return session.get(LibraryRecord, record_id)

    @staticmethod
    def get_by_identity(session: Session, recording_id: str, release_id: str) -> LibraryRecord | None:
        return session.scalar(
            select(LibraryRecord)
            .where(LibraryRecord.musicbrainz_recording_id == recording_id)
            .where(LibraryRecord.musicbrainz_release_id == release_id)
        )


@final
class LibraryRecordConsolidationRecord(Base):
    """Append-only alias retained when exact content joins another aggregate."""

    __tablename__ = 'library_record_consolidations'

    retired_library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), primary_key=True)
    canonical_library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    sha256: Mapped[str] = mapped_column(Text, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


@final
class LibraryPublicationRecord(Base):
    """Immutable managed output version tied to the exact source version used."""

    __tablename__ = 'library_publications'

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    path: Mapped[str] = mapped_column(Text, nullable=False)
    format_name: Mapped[str] = mapped_column(Text, nullable=False)
    content_sha256: Mapped[str] = mapped_column(Text, nullable=False)
    metadata_revision_id: Mapped[int | None] = mapped_column(ForeignKey('library_metadata_revisions.id'))
    state: Mapped[str] = mapped_column(Text, nullable=False, default='current')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(
        back_populates='publications', foreign_keys='LibraryPublicationRecord.library_record_id'
    )
    source: Mapped[SourceRecordView] = relationship('SourceRecord', back_populates='library_publications')
    metadata_revision: Mapped[LibraryMetadataRevisionRecord | None] = relationship(back_populates='publications')


@final
class ReleaseArtworkRecord(Base):
    """Current managed artwork state for one MusicBrainz release."""

    __tablename__ = 'release_artwork'

    release_mbid: Mapped[str] = mapped_column(Text, primary_key=True)
    path: Mapped[str | None] = mapped_column(Text)
    format_name: Mapped[str | None] = mapped_column(Text)
    provider: Mapped[str] = mapped_column(Text, nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


_ = Index(
    'uq_current_library_publication',
    LibraryPublicationRecord.library_record_id,
    unique=True,
    postgresql_where=LibraryPublicationRecord.state == 'current',
    sqlite_where=LibraryPublicationRecord.state == 'current',
)

_ = Index(
    'uq_current_library_publication_path',
    LibraryPublicationRecord.path,
    unique=True,
    postgresql_where=LibraryPublicationRecord.state == 'current',
    sqlite_where=LibraryPublicationRecord.state == 'current',
)


@final
class EffectiveSourceDecisionRecord(Base):
    """Persisted policy result for one stable recording aggregate."""

    __tablename__ = 'effective_source_decisions'

    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), primary_key=True)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    baseline_source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    policy_version: Mapped[str] = mapped_column(Text, nullable=False)
    quality_tuple_json: Mapped[str] = mapped_column(Text, nullable=False)
    reason: Mapped[str] = mapped_column(Text, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(back_populates='effective_source_decision')


@final
class PublicationAttemptRecord(Base):
    """Durable filesystem publication attempt state for crash recovery."""

    __tablename__ = 'publication_attempts'

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str] = mapped_column(ForeignKey('source_records.id'), nullable=False)
    metadata_revision_id: Mapped[int | None] = mapped_column(ForeignKey('library_metadata_revisions.id'))
    state: Mapped[str] = mapped_column(Text, nullable=False)
    target_directory: Mapped[str] = mapped_column(Text, nullable=False)
    target_audio_name: Mapped[str] = mapped_column(Text, nullable=False)
    staging_directory: Mapped[str] = mapped_column(Text, nullable=False)
    backup_directory: Mapped[str] = mapped_column(Text, nullable=False)
    manifest_sha256: Mapped[str | None] = mapped_column(Text)
    output_sha256: Mapped[str | None] = mapped_column(Text)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    exposed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    finalized_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    failure_reason: Mapped[str | None] = mapped_column(Text)
    cleaned_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    completion_state: Mapped[str] = mapped_column(Text, nullable=False, default='complete')

    library_record: Mapped[LibraryRecord] = relationship(back_populates='publication_attempts')


_ = Index(
    'ix_pending_publication_intent',
    PublicationAttemptRecord.library_record_id,
    PublicationAttemptRecord.source_id,
    PublicationAttemptRecord.metadata_revision_id,
    postgresql_where=PublicationAttemptRecord.state.in_(['reserved', 'staged', 'prepared', 'exposed']),
    sqlite_where=PublicationAttemptRecord.state.in_(['reserved', 'staged', 'prepared', 'exposed']),
)


@final
class LibraryMetadataRevisionRecord(Base):
    """Append-only metadata layer attached to a stable library record."""

    __tablename__ = 'library_metadata_revisions'
    __table_args__: tuple[UniqueConstraint, ...] = (UniqueConstraint('library_record_id', 'layer', 'revision'),)

    id: Mapped[int] = mapped_column(primary_key=True)
    library_record_id: Mapped[str] = mapped_column(ForeignKey('library_records.id'), nullable=False)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    layer: Mapped[str] = mapped_column(Text, nullable=False)
    revision: Mapped[int] = mapped_column(nullable=False)
    tags_json: Mapped[str] = mapped_column(Text, nullable=False)
    actor: Mapped[str] = mapped_column(Text, nullable=False)
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
    kind: Mapped[str] = mapped_column(Text, nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    reason: Mapped[str | None] = mapped_column(Text)
    details_json: Mapped[str] = mapped_column(Text, nullable=False, default='{}')
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    library_record: Mapped[LibraryRecord] = relationship(back_populates='events')
