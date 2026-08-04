from __future__ import annotations

from datetime import datetime
from typing import final

from sqlalchemy import DateTime, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship

from music_ingest.persistence.base import Base


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
    started_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    job: Mapped[JobRecord] = relationship(back_populates='attempts')
