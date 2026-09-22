from __future__ import annotations

from datetime import datetime
from typing import final

from sqlalchemy import CheckConstraint, DateTime, ForeignKey, Index, Integer, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, Session, mapped_column, relationship

from music_ingest.models.db import Base


@final
class WebhookReceiptRecord(Base):
    __tablename__ = 'webhook_receipts'

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    event_fingerprint: Mapped[str] = mapped_column(Text, unique=True, nullable=False)
    provider_name: Mapped[str] = mapped_column(Text, nullable=False)
    payload_json: Mapped[str] = mapped_column(Text, nullable=False)
    received_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    job_id: Mapped[str | None] = mapped_column(ForeignKey('jobs.id'))
    job: Mapped[JobRecord | None] = relationship(back_populates='webhook_receipts')


@final
class JobRecord(Base):
    __tablename__ = 'jobs'
    __table_args__: tuple[CheckConstraint, ...] = (
        CheckConstraint(
            '(CASE WHEN source_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN library_record_id IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN release_mbid IS NOT NULL THEN 1 ELSE 0 END + '
            + 'CASE WHEN folder_path IS NOT NULL THEN 1 ELSE 0 END) = 1 OR '
            + "kind = 'reconciliation_scan'",
            name='ck_jobs_target_or_reconciliation',
        ),
    )

    id: Mapped[str] = mapped_column(Text, primary_key=True)
    source_id: Mapped[str | None] = mapped_column(ForeignKey('source_records.id'))
    library_record_id: Mapped[str | None] = mapped_column(ForeignKey('library_records.id'))
    release_mbid: Mapped[str | None] = mapped_column(Text)
    folder_path: Mapped[str | None] = mapped_column(Text)
    kind: Mapped[str] = mapped_column(Text, nullable=False)
    metadata_revision_id: Mapped[int | None] = mapped_column(ForeignKey('library_metadata_revisions.id'))
    state: Mapped[str] = mapped_column(Text, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    next_attempt_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    failure_reason: Mapped[str | None] = mapped_column(Text)
    result_json: Mapped[str | None] = mapped_column(Text)
    source_metadata_revision: Mapped[int | None] = mapped_column(Integer)
    attempts: Mapped[list[JobAttemptRecord]] = relationship(
        back_populates='job', lazy='selectin', order_by='JobAttemptRecord.attempt_number'
    )
    webhook_receipts: Mapped[list[WebhookReceiptRecord]] = relationship(back_populates='job', lazy='selectin')

    @staticmethod
    def get(session: Session, job_id: str) -> JobRecord | None:
        return session.get(JobRecord, job_id)


_ = Index(
    'uq_active_selection_refresh_job',
    JobRecord.library_record_id,
    JobRecord.kind,
    unique=True,
    postgresql_where=(JobRecord.kind == 'selection_refresh') & JobRecord.state.in_(['queued', 'running']),
    sqlite_where=(JobRecord.kind == 'selection_refresh') & JobRecord.state.in_(['queued', 'running']),
)

_ = Index(
    'uq_active_lrclib_fetch_job',
    JobRecord.library_record_id,
    JobRecord.kind,
    unique=True,
    postgresql_where=(JobRecord.kind == 'lrclib_fetch') & JobRecord.state.in_(['queued', 'running']),
    sqlite_where=(JobRecord.kind == 'lrclib_fetch') & JobRecord.state.in_(['queued', 'running']),
)

_ = Index(
    'uq_active_folder_release_selection_job',
    JobRecord.folder_path,
    JobRecord.kind,
    unique=True,
    postgresql_where=(JobRecord.kind == 'folder_release_selection') & JobRecord.state.in_(['queued', 'running']),
    sqlite_where=(JobRecord.kind == 'folder_release_selection') & JobRecord.state.in_(['queued', 'running']),
)

_ = Index(
    'uq_active_reconciliation_scan_job',
    JobRecord.kind,
    unique=True,
    postgresql_where=(JobRecord.kind == 'reconciliation_scan') & JobRecord.state.in_(['queued', 'running']),
    sqlite_where=(JobRecord.kind == 'reconciliation_scan') & JobRecord.state.in_(['queued', 'running']),
)


@final
class JobAttemptRecord(Base):
    __tablename__ = 'job_attempts'
    __table_args__: tuple[UniqueConstraint, ...] = (
        UniqueConstraint('job_id', 'attempt_number', name='uq_job_attempt_number'),
    )

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    job_id: Mapped[str] = mapped_column(ForeignKey('jobs.id'), nullable=False)
    attempt_number: Mapped[int] = mapped_column(Integer, nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False)
    started_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    job: Mapped[JobRecord] = relationship(back_populates='attempts')
