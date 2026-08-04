from __future__ import annotations

import json
import uuid
from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from music_ingest.persistence.library import (
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
)
from music_ingest.persistence.models import SourceRecord


def new_library_record(session: Session, now: datetime | None = None) -> LibraryRecord:
    """Create one stable record before any source file is processed."""
    timestamp = now or datetime.now(UTC)
    record = LibraryRecord(id=f'record-{uuid.uuid4().hex}', created_at=timestamp, updated_at=timestamp)
    session.add(record)
    session.flush()
    return record


def ensure_source_record(session: Session, source: SourceRecord, now: datetime) -> LibraryRecord:
    """Return the stable record for a source, creating one for legacy intake rows."""
    if source.library_record is not None:
        return source.library_record
    record = new_library_record(session, now)
    source.library_record = record
    session.flush()
    return record


def attach_source(
    session: Session,
    source_id: str,
    library_record_id: str,
    *,
    reason: str = 'source_attached',
    now: datetime | None = None,
) -> SourceRecord:
    """Attach an immutable source version to an existing stable library record."""
    source = session.scalar(select(SourceRecord).where(SourceRecord.id == source_id))
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if source is None or record is None:
        raise LookupError(source_id if source is None else library_record_id)
    timestamp = now or datetime.now(UTC)
    source.library_record = record
    source.disappeared_at = None
    record.source_state = 'present'
    for publication in record.publications:
        if publication.state == 'current':
            publication.state = 'superseded'
    if record.publication_state == 'current':
        record.publication_state = 'stale'
    record.updated_at = timestamp
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source.id,
            kind='source_attached',
            state='present',
            reason=reason,
            details_json='{}',
            created_at=timestamp,
        )
    )
    session.flush()
    return source


def record_publication(
    session: Session,
    library_record_id: str,
    source_id: str,
    published_path: Path,
    content_sha256: str,
    metadata_revision_id: int | None,
    now: datetime,
) -> LibraryPublicationRecord:
    """Append a current publication version and supersede the previous one."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    for previous in record.publications:
        if previous.state == 'current':
            previous.state = 'superseded'
    publication = LibraryPublicationRecord(
        id=f'publication-{uuid.uuid4().hex}',
        library_record_id=record.id,
        source_id=source_id,
        path=str(published_path),
        format_name=published_path.suffix.removeprefix('.'),
        content_sha256=content_sha256,
        metadata_revision_id=metadata_revision_id,
        state='current',
        created_at=now,
    )
    record.publication_state = 'current'
    record.processing_state = 'complete'
    record.updated_at = now
    session.add(publication)
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source_id,
            kind='publication_created',
            state='current',
            reason=None,
            details_json=json.dumps({'publication_id': publication.id}, sort_keys=True),
            created_at=now,
        )
    )
    session.flush()
    return publication


def record_metadata_layers(
    session: Session,
    library_record_id: str,
    source_id: str,
    original: dict[str, str],
    analyzed: dict[str, str],
    final: dict[str, str],
    now: datetime,
) -> tuple[LibraryMetadataRevisionRecord, ...]:
    """Persist immutable original, analyzed, and final metadata layers."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    revisions: list[LibraryMetadataRevisionRecord] = []
    for layer, tags in (('original', original), ('analyzed', analyzed), ('final', final)):
        next_revision = (
            max(
                (item.revision for item in record.metadata_revisions if item.layer == layer),
                default=0,
            )
            + 1
        )
        revision = LibraryMetadataRevisionRecord(
            library_record_id=record.id,
            source_id=source_id,
            layer=layer,
            revision=next_revision,
            tags_json=json.dumps(tags, ensure_ascii=False, sort_keys=True),
            actor='worker',
            created_at=now,
        )
        revisions.append(revision)
        session.add(revision)
    record.metadata_state = 'final'
    record.updated_at = now
    session.flush()
    return tuple(revisions)


def record_event(
    session: Session,
    library_record_id: str,
    kind: str,
    state: str,
    reason: str | None,
    now: datetime,
    source_id: str | None = None,
) -> None:
    """Append a state explanation to a stable library record."""
    record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == library_record_id))
    if record is None:
        raise LookupError(library_record_id)
    record.processing_state = state
    record.updated_at = now
    session.add(
        LibraryEventRecord(
            library_record_id=record.id,
            source_id=source_id,
            kind=kind,
            state=state,
            reason=reason,
            details_json='{}',
            created_at=now,
        )
    )
    session.flush()


def library_record_detail(session: Session, library_record_id: str) -> LibraryRecord:
    """Load one stable record with its source and publication history."""
    record = session.scalar(
        select(LibraryRecord)
        .where(LibraryRecord.id == library_record_id)
        .options(
            selectinload(LibraryRecord.sources),
            selectinload(LibraryRecord.publications),
            selectinload(LibraryRecord.metadata_revisions),
            selectinload(LibraryRecord.events),
        )
    )
    if record is None:
        raise LookupError(library_record_id)
    return record


def library_records(session: Session) -> list[LibraryRecord]:
    """Load stable records for the source/publication catalog."""
    return list(
        session.scalars(
            select(LibraryRecord).options(
                selectinload(LibraryRecord.sources),
                selectinload(LibraryRecord.publications),
            )
        ).all()
    )
