from __future__ import annotations

import json
import re
import uuid
from datetime import UTC, datetime
from enum import StrEnum
from typing import Final
from urllib.parse import urlparse

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from music_ingest.persistence.models import PublishSnapshotRecord, ReviewAuditRecord, ReviewReleaseRecord, SourceRecord

LOCAL_NAMESPACE: Final = uuid.UUID('d5e4f3e2-6e9c-4b2f-a1a8-45dfe7a9b5c7')
_MBID_RE: Final = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')


class QueueState(StrEnum):
    DISCOVERED = 'discovered'
    SANITIZED = 'sanitized'
    MATCHED = 'matched'
    NEEDS_REVIEW = 'needs_review'
    READY = 'ready'
    PUBLISHED = 'published'
    REJECTED = 'rejected'


class ReviewInputError(ValueError):
    """Raised when reviewer-provided external identifiers are unsafe or malformed."""


def queue(session: Session, state: QueueState | None = None) -> list[ReviewReleaseRecord]:
    records = session.scalars(select(ReviewReleaseRecord).order_by(ReviewReleaseRecord.source_id)).all()
    if state is None:
        return list(records)
    return [record for record in records if record.state == state.value]


def get_or_create(session: Session, source_id: str) -> ReviewReleaseRecord:
    source = session.scalar(
        select(SourceRecord)
        .where(SourceRecord.id == source_id)
        .options(
            selectinload(SourceRecord.tag_observations),
            selectinload(SourceRecord.provider_attempts),
            selectinload(SourceRecord.candidates),
            selectinload(SourceRecord.fingerprints),
            selectinload(SourceRecord.review_release),
            selectinload(SourceRecord.review_audits),
            selectinload(SourceRecord.publish_snapshots),
        )
    )
    if source is None:
        raise LookupError(source_id)
    if source.review_release is not None:
        return source.review_release
    original = _original_fields(source)
    record = ReviewReleaseRecord(
        source_id=source.id,
        state=QueueState.NEEDS_REVIEW.value,
        original_json=_dump(original),
        proposed_json=_dump(original),
        artist_id=_local_id(source.id, 'artist'),
        release_id=_local_id(source.id, 'release'),
        track_id=_local_id(source.id, 'track'),
    )
    source.intake_state = QueueState.NEEDS_REVIEW.value
    session.add(record)
    session.flush()
    return record


def perform_action(
    session: Session,
    source_id: str,
    action: str,
    fields: dict[str, str],
    actor: str = 'local-reviewer',
) -> ReviewReleaseRecord:
    record = get_or_create(session, source_id)
    before = _release_payload(record)
    if action == 'attach':
        mbid = _parse_mbid(fields.get('musicbrainz_id', ''))
        _snapshot_if_published(session, record)
        record.musicbrainz_id = mbid
        record.state = QueueState.NEEDS_REVIEW.value
    elif action == 'reject':
        record.state = QueueState.REJECTED.value
    elif action == 'defer':
        record.state = QueueState.NEEDS_REVIEW.value
    elif action == 'rematch':
        _snapshot_if_published(session, record)
        record.musicbrainz_id = None
        record.state = QueueState.NEEDS_REVIEW.value
    else:
        raise ReviewInputError(f'unsupported action: {action}')
    after = _release_payload(record)
    session.add(
        ReviewAuditRecord(
            source_id=source_id,
            action=action.replace('-', '_'),
            before_json=_dump(before),
            after_json=_dump(after),
            actor=actor,
            created_at=datetime.now(UTC),
        )
    )
    session.flush()
    return record


def _original_fields(source: SourceRecord) -> dict[str, str]:
    values = {item.tag_name.upper(): item.value for item in source.tag_observations}
    return {'artist': values.get('ARTIST', ''), 'release_title': values.get('ALBUM', values.get('TITLE', ''))}


def _snapshot_if_published(session: Session, record: ReviewReleaseRecord) -> None:
    if record.state != QueueState.READY.value:
        return
    session.add(
        PublishSnapshotRecord(
            source_id=record.source_id,
            state=record.state,
            release_json=record.proposed_json,
            captured_at=datetime.now(UTC),
        )
    )


def _parse_mbid(value: str) -> str:
    candidate = value.strip()
    parsed = urlparse(candidate)
    if parsed.scheme == 'https' and parsed.netloc == 'musicbrainz.org':
        candidate = parsed.path.rstrip('/').split('/')[-1]
    if not _MBID_RE.fullmatch(candidate.lower()):
        raise ReviewInputError('expected a MusicBrainz UUID or musicbrainz.org URL')
    return candidate.lower()


def _local_id(source_id: str, kind: str) -> str:
    return f'local-{kind}-{uuid.uuid5(LOCAL_NAMESPACE, f"{source_id}:{kind}").hex}'


def _release_payload(record: ReviewReleaseRecord) -> dict[str, str | None]:
    return {
        'state': record.state,
        'musicbrainz_id': record.musicbrainz_id,
        'proposed': record.proposed_json,
        'artist_id': record.artist_id,
        'release_id': record.release_id,
        'track_id': record.track_id,
    }


def _dump(value: dict[str, str | None] | dict[str, str]) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
