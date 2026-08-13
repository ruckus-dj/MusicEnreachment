from __future__ import annotations

import re
import secrets
from collections.abc import Iterable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import final, override

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session, selectinload

from music_ingest.models.entities import (
    DecoderEvidenceRecord,
    FingerprintRecord,
    ProviderScheduleRecord,
    ProviderSnapshotRecord,
    SourceRecord,
    WebhookReceiptRecord,
)


@final
class IntakeRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def find_source(self, source_id: str) -> SourceRecord | None:
        return self._session.scalar(
            select(SourceRecord)
            .where(SourceRecord.id == source_id)
            .options(
                selectinload(SourceRecord.tag_observations),
                selectinload(SourceRecord.artwork_observations),
                selectinload(SourceRecord.provider_attempts),
                selectinload(SourceRecord.candidates),
                selectinload(SourceRecord.review_decisions),
                selectinload(SourceRecord.fingerprints),
            )
        )

    def find_other_source_by_sha256(self, sha256: str, source_id: str) -> SourceRecord | None:
        return self._session.scalar(
            select(SourceRecord)
            .where(SourceRecord.sha256 == sha256)
            .where(SourceRecord.id != source_id)
            .where(SourceRecord.library_record_id.is_not(None))
            .order_by(SourceRecord.id)
            .limit(1)
        )

    def add_source(self, source: SourceRecord) -> SourceRecord:
        self._session.add(source)
        self._session.flush()
        return source


@final
class FingerprintRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def add_evidence(self, evidence: FingerprintRecord) -> FingerprintRecord:
        self._session.add(evidence)
        self._session.flush()
        return evidence

    def successful_evidence(self, source_id: str) -> FingerprintRecord | None:
        return self._session.scalar(
            select(FingerprintRecord)
            .where(FingerprintRecord.source_id == source_id)
            .where(FingerprintRecord.state == 'success')
            .order_by(FingerprintRecord.id.desc())
        )


@final
class DecoderEvidenceRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def successful_evidence(self, source_id: str, decoder_command: str) -> DecoderEvidenceRecord | None:
        return self._session.scalar(
            select(DecoderEvidenceRecord)
            .where(DecoderEvidenceRecord.source_id == source_id)
            .where(DecoderEvidenceRecord.decoder_command == decoder_command)
            .where(DecoderEvidenceRecord.tool_state == 'success')
            .order_by(DecoderEvidenceRecord.id.desc())
        )

    def add_evidence(self, evidence: DecoderEvidenceRecord) -> DecoderEvidenceRecord:
        self._session.add(evidence)
        self._session.flush()
        return evidence


@dataclass(frozen=True, slots=True)
class WebhookReceiptInput:
    event_fingerprint: str
    provider_name: str
    payload_json: str
    received_at: datetime
    job_id: str | None = None


@dataclass(frozen=True, slots=True)
class ReceiptReplayConflictError(Exception):
    event_fingerprint: str

    @override
    def __str__(self) -> str:
        return f'event fingerprint {self.event_fingerprint} conflicts with an existing receipt'


@final
class WebhookReceiptRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def record_or_reuse(self, receipt: WebhookReceiptInput) -> WebhookReceiptRecord:
        existing = self._session.scalar(
            select(WebhookReceiptRecord).where(WebhookReceiptRecord.event_fingerprint == receipt.event_fingerprint)
        )
        if existing is not None:
            self._ensure_same_receipt(existing, receipt)
            return existing
        try:
            with self._session.begin_nested():
                persisted = WebhookReceiptRecord(
                    event_fingerprint=receipt.event_fingerprint,
                    provider_name=receipt.provider_name,
                    payload_json=receipt.payload_json,
                    received_at=receipt.received_at,
                    job_id=receipt.job_id,
                )
                self._session.add(persisted)
                self._session.flush()
        except IntegrityError:
            existing = self._session.scalar(
                select(WebhookReceiptRecord).where(WebhookReceiptRecord.event_fingerprint == receipt.event_fingerprint)
            )
            if existing is None:
                raise
            self._ensure_same_receipt(existing, receipt)
            return existing
        return persisted

    @staticmethod
    def _ensure_same_receipt(existing: WebhookReceiptRecord, receipt: WebhookReceiptInput) -> None:
        if (
            existing.provider_name != receipt.provider_name
            or existing.payload_json != receipt.payload_json
            or existing.job_id != receipt.job_id
        ):
            raise ReceiptReplayConflictError(receipt.event_fingerprint)


_HASH_RE = re.compile(r'^[0-9a-f]{64}$')
_FORBIDDEN_DESCRIPTOR_RE = re.compile(
    r'(?i)(user[-_ ]?agent|contact|acoustid[-_ ]?(?:key|api)|fingerprint|exception|authorization|api[-_ ]?key)'
)


def ensure_provider_schedules(session: Session, provider_names: Iterable[str], next_start_at: datetime) -> None:
    """Create missing provider schedules without changing existing runtime state."""
    for provider_name in provider_names:
        if session.get(ProviderScheduleRecord, provider_name) is not None:
            continue
        try:
            with session.begin_nested():
                session.add(ProviderScheduleRecord(provider_name=provider_name, next_start_at=next_start_at))
                session.flush()
        except IntegrityError:
            continue


@final
class ProviderPersistenceRepository:
    def __init__(self, session: Session) -> None:
        self._session: Session = session

    def append_snapshot(
        self,
        *,
        provider_name: str,
        request_hash: str,
        request_descriptor: str,
        response_sha256: str,
        response_body: bytes | None = None,
        captured_at: datetime,
        outcome: str,
        state: str,
        age_seconds: int | None = None,
        http_status: int | None = None,
    ) -> ProviderSnapshotRecord:
        _require_utc(captured_at, 'captured_at')
        if not provider_name or not outcome or not state or not request_descriptor:
            raise ValueError('provider name, descriptor, outcome, and state are required')
        if not _HASH_RE.fullmatch(request_hash) or not _HASH_RE.fullmatch(response_sha256):
            raise ValueError('request and response hashes must be lowercase SHA-256 hex')
        has_credentialed_url = '://' in request_descriptor and '@' in request_descriptor
        if _FORBIDDEN_DESCRIPTOR_RE.search(request_descriptor) or has_credentialed_url:
            raise ValueError('request descriptor contains forbidden sensitive data')
        if http_status is not None and not 100 <= http_status <= 599:
            raise ValueError('HTTP status must be between 100 and 599')
        if age_seconds is not None and age_seconds < 0:
            raise ValueError('age seconds must be non-negative')
        snapshot = ProviderSnapshotRecord(
            provider_name=provider_name,
            request_hash=request_hash,
            request_descriptor=request_descriptor,
            response_sha256=response_sha256,
            response_body=response_body,
            captured_at=captured_at,
            outcome=outcome,
            state=state,
            age_seconds=age_seconds,
            http_status=http_status,
        )
        self._session.add(snapshot)
        self._session.flush()
        return snapshot

    def newest_relevant(self, provider_name: str, request_hash: str) -> ProviderSnapshotRecord | None:
        return self._session.scalar(
            select(ProviderSnapshotRecord)
            .where(ProviderSnapshotRecord.provider_name == provider_name)
            .where(ProviderSnapshotRecord.request_hash == request_hash)
            .order_by(ProviderSnapshotRecord.captured_at.desc(), ProviderSnapshotRecord.id.desc())
            .limit(1)
        )

    def reserve_next_start(
        self, provider_name: str, now: datetime, interval: timedelta, lease_duration: timedelta
    ) -> ProviderReservation:
        _require_utc(now, 'now')
        if interval <= timedelta(0) or lease_duration <= timedelta(0):
            raise ValueError('interval and lease duration must be positive')
        schedule = self._session.scalar(
            select(ProviderScheduleRecord)
            .where(ProviderScheduleRecord.provider_name == provider_name)
            .with_for_update()
        )
        if schedule is None:
            raise LookupError(f'provider schedule is not seeded: {provider_name}')
        scheduled_start = _as_utc(schedule.next_start_at, 'next_start_at')
        reserved_start = max(scheduled_start, now)
        schedule.next_start_at = reserved_start + interval
        lease_until = now + lease_duration
        lease_token = secrets.token_hex(32)
        schedule.lease_until = lease_until
        schedule.lease_token = lease_token
        self._session.flush()
        return ProviderReservation(
            scheduled_start=reserved_start,
            lease_token=lease_token,
            lease_until=lease_until,
        )

    def validate_lease(self, provider_name: str, lease_token: str, now: datetime) -> bool:
        _require_utc(now, 'now')
        schedule = self._session.get(ProviderScheduleRecord, provider_name)
        if schedule is None or schedule.lease_token is None or schedule.lease_until is None:
            return False
        lease_until = _as_utc(schedule.lease_until, 'lease_until')
        return secrets.compare_digest(schedule.lease_token, lease_token) and now < lease_until


@dataclass(frozen=True, slots=True)
class ProviderReservation:
    scheduled_start: datetime
    lease_token: str
    lease_until: datetime


def _require_utc(value: datetime, field_name: str) -> None:
    if value.tzinfo is None or value.utcoffset() is None:
        raise ValueError(f'{field_name} must be UTC-aware')
    if value.utcoffset() != timedelta(0):
        raise ValueError(f'{field_name} must use UTC')


def _as_utc(value: datetime, field_name: str) -> datetime:
    if value.tzinfo is None or value.utcoffset() is None:
        # SQLite does not preserve timezone metadata; the schema contract is UTC.
        return value.replace(tzinfo=UTC)
    if value.utcoffset() != timedelta(0):
        raise ValueError(f'{field_name} must use UTC')
    return value
