from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path

from fastapi import HTTPException
from sqlalchemy import select
from sqlalchemy.orm import Session, raiseload

from music_ingest.models import (
    JobRecord,
    LibraryRecord,
    SourceRecord,
)
from music_ingest.models.library import SourceRecordView
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import (
    record_event,
)
from music_ingest.services.source_boundary import SourceBoundaryError, resolve_owned_source


def destination_conflict(
    session: Session, record: LibraryRecord, source: SourceRecordView, media_root: Path | None
) -> dict[str, str] | None:
    if media_root is None:
        return None
    publications = [item for item in record.publications if item.state == 'current']
    if any(Path(item.path).resolve().exists() for item in publications):
        return None
    jobs = list(
        session.scalars(
            select(JobRecord)
            .where(
                JobRecord.source_id == source.id,
                JobRecord.kind.not_in(['acoustid_analysis', 'musicbrainz_analysis', 'final_publish']),
            )
            .options(raiseload('*'))
            .order_by(JobRecord.created_at.desc())
        ).all()
    )
    if not jobs:
        return None
    candidate = (media_root.resolve() / jobs[0].id).resolve()
    if candidate == media_root.resolve() or media_root.resolve() not in candidate.parents or not candidate.exists():
        return None
    ownership = 'managed' if jobs[0].id == candidate.name and jobs[0].kind == 'filesystem_scan' else 'unmanaged'
    return {'path': str(candidate), 'ownership': ownership, 'reason': 'media destination already exists'}


def queue_source_recovery(
    session: Session, record: LibraryRecord, source: SourceRecordView, now: datetime
) -> str | None:
    _ = require_owned_source(session, source.id)
    if source.disappeared_at is not None:
        return None
    current_publication = next((item for item in record.publications if item.state == 'current'), None)

    final_revision = next(
        (item for item in reversed(record.metadata_revisions) if item.source_id == source.id and item.layer == 'final'),
        None,
    )
    analyzed_revision = next(
        (
            item
            for item in reversed(record.metadata_revisions)
            if item.source_id == source.id and item.layer == 'analyzed'
        ),
        None,
    )
    if record.processing_state == 'complete':
        if analyzed_revision is None or not any(
            not name.startswith('MUSICBRAINZ_') for name in json.loads(analyzed_revision.tags_json)
        ):
            return None
        kind = 'acoustid_analysis'
    elif current_publication is None:
        kind = 'final_publish' if final_revision is not None else 'filesystem_scan'
    elif record.processing_state == 'publishing' or record.publication_state in {'stale', 'failed'}:
        kind = 'final_publish'
    elif record.processing_state != 'complete':
        kind = 'acoustid_analysis'
    else:
        return None
    job = JobRepository(session).enqueue(
        source.id,
        kind,
        now,
        final_revision.id if kind == 'final_publish' and final_revision is not None else None,
    )
    if job is None:
        return None
    record_event(
        session,
        record.id,
        'manual_recovery_queued',
        'publishing' if kind == 'final_publish' else 'queued',
        f'manual recovery queued {kind}',
        now,
        source.id,
    )
    return kind


def require_owned_source(session: Session, source_id: str) -> SourceRecord:
    persisted_source = session.get(SourceRecord, source_id)
    if persisted_source is None:
        raise HTTPException(status_code=404, detail='source not found')
    try:
        _ = resolve_owned_source(persisted_source)
    except SourceBoundaryError as error:
        raise HTTPException(status_code=409, detail=f'source root boundary: {error}') from error
    return persisted_source


def queue_record_recovery(
    session: Session, record: LibraryRecord, now: datetime, media_root: Path | None
) -> tuple[int, int]:
    queued = 0
    conflicts = 0
    for source in record.sources:
        if source.disappeared_at is not None:
            continue
        if destination_conflict(session, record, source, media_root) is not None:
            conflicts += 1
            continue
        if queue_source_recovery(session, record, source, now) is not None:
            queued += 1
    return queued, conflicts
