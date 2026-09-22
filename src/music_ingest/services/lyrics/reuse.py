"""Durable, conservative lyric evidence independent of the current publication binding.

No lyric text is stored here. Reuse requires the original managed file and its hash.
Exact duration is intentional: even a one-second change triggers fresh provider validation.
"""

from dataclasses import asdict
from datetime import datetime
from hashlib import sha256
from pathlib import Path
from typing import Literal

from pydantic import BaseModel, ConfigDict
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.adapters.external.lrclib import LrclibLookupRequest, LrclibProvenance, LrclibSettings
from music_ingest.models import LibraryRecord, SourceRecord
from music_ingest.models.entities import SourceRecordingAssignmentRecord
from music_ingest.services.lyrics.validate import SyncedLyricsValid, validate_synced_lyrics


class LyricEvidence(BaseModel):
    model_config = ConfigDict(frozen=True, extra='forbid')

    input_hash: str
    publication_id: str
    outcome: Literal['synced', 'no_candidate']
    provenance: LrclibProvenance
    reason: str
    path: str | None = None
    sha256: str | None = None
    expires_at: datetime | None = None


def reuse_key(
    session: Session,
    record: LibraryRecord,
    source: SourceRecord,
    lookup: LrclibLookupRequest,
    settings: LrclibSettings,
) -> str:
    import json

    assignment = session.scalar(
        select(SourceRecordingAssignmentRecord)
        .where(SourceRecordingAssignmentRecord.source_id == source.id)
        .order_by(SourceRecordingAssignmentRecord.id.desc())
        .limit(1)
    )
    # Bare metadata / operator-entered MBIDs are not verification of an encoding.
    verified = (
        record.match_state == 'matched'
        and record.musicbrainz_recording_id is not None
        and source.library_record_id == record.id
        and assignment is not None
        and assignment.library_record_id == record.id
        and assignment.state == 'automatic_verified'
    )
    identity = {'recording': record.musicbrainz_recording_id, 'release': record.musicbrainz_release_id}
    if not verified:
        identity['source_sha256'] = source.sha256
    payload = {'policy': 1, 'identity': identity, 'lookup': asdict(lookup), 'settings': asdict(settings)}
    return sha256(json.dumps(payload, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def read_validated_sidecar(evidence: LyricEvidence, root: Path, duration: int) -> str | None:
    if evidence.path is None or evidence.sha256 is None:
        return None
    try:
        root = root.resolve(strict=True)
        relative = Path(evidence.path)
        if relative.is_absolute() or '..' in relative.parts:
            return None
        path = root / relative
        # Do not follow symlinks, including intermediate directories, even within media.
        if any(part.is_symlink() for part in (path, *path.parents)):
            return None
        if root not in path.resolve(strict=True).parents:
            return None
        with path.open('rb') as handle:
            payload = handle.read(4 * 1024 * 1024 + 1)
        if len(payload) > 4 * 1024 * 1024 or sha256(payload).hexdigest() != evidence.sha256:
            return None
        result = validate_synced_lyrics(payload, float(duration))
        return result.text if isinstance(result, SyncedLyricsValid) else None
    except OSError, ValueError:
        return None
