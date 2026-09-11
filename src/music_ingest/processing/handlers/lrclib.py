"""Synced-lyrics fetch for the current publication, written as a validated managed sidecar."""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Final

from pydantic import TypeAdapter
from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.external.lrclib import (
    LrclibLookupRequest,
    LrclibNoCandidate,
    LrclibProvenance,
    LrclibProviderError,
    LrclibSynced,
)
from music_ingest.lyrics.validate import (
    LrcWriteRejected,
    LrcWriteRequest,
    LrcWritten,
    SyncedLyricsInvalid,
    SyncedLyricsValid,
    validate_synced_lyrics,
    write_lrc_atomically,
)
from music_ingest.models import (
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    SourceRecord,
)
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing.candidates import _persisted_duration_seconds
from music_ingest.processing.execution import ExecutionContext, HandlerOutcome, ProcessingInfrastructureError

_TAGS_ADAPTER = TypeAdapter(dict[str, str])

_SYNCED: Final = 'synced'
_NONE: Final = 'none'
_NO_CANDIDATE: Final = 'no_candidate'
_VALIDATION_REJECTED: Final = 'validation_rejected'
_ERROR: Final = 'error'
_INVALID_UTF8_REASON: Final = 'synced lyrics are not valid UTF-8'
_DISABLED_REASON: Final = 'lrclib provider is disabled in runtime settings'

_EVENT_KINDS: Final[dict[str, str]] = {
    _SYNCED: 'lrclib_fetch_synced',
    _NONE: 'lrclib_fetch_skipped',
    _NO_CANDIDATE: 'lrclib_fetch_no_candidate',
    _VALIDATION_REJECTED: 'lrclib_fetch_validation_rejected',
    _ERROR: 'lrclib_fetch_error',
}


class LrclibAdapterNotConfiguredError(ProcessingInfrastructureError):
    """A fetch ran without the configured adapter: a runtime wiring defect, not a provider condition.

    The runtime must inject the adapter it builds from persisted settings, so reaching a fetch without one means the
    process was composed wrongly. The job stays retryable and the reason names the defect instead of blaming lrclib.
    """


@dataclass(frozen=True, slots=True)
class LrclibHandler:
    """Resolve synced lyrics for the claimed record against its *current* publication.

    The fetch always resolves the record's current publication instead of the publication that queued the job, so
    a fetch coalesced across a supersede still binds lyrics to the newest managed output. Only text that crossed
    ``validate_synced_lyrics`` reaches the managed sidecar: lyric text is never persisted in a database column,
    event detail, or API payload. A terminal outcome materializes the whole lyric state of the record plus one
    human-history event in the worker's savepoint, and an existing sidecar is replaced only on success.
    """

    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> HandlerOutcome:
        session = context.session
        record_id = claimed.job.library_record_id
        if record_id is None:
            raise ProcessingInfrastructureError('lrclib fetch requires a library record target')
        record = session.scalar(select(LibraryRecord).where(LibraryRecord.id == record_id).with_for_update())
        if record is None:
            raise ProcessingInfrastructureError('lrclib fetch library record is missing')
        adapter = context.config.lrclib_adapter
        if adapter is None:
            raise LrclibAdapterNotConfiguredError('lrclib provider is not configured for the runtime')
        if not adapter.enabled:
            self._skip_disabled(context, record)
            return None
        publication = session.scalar(
            select(LibraryPublicationRecord)
            .where(LibraryPublicationRecord.library_record_id == record.id)
            .where(LibraryPublicationRecord.state == 'current')
            .order_by(LibraryPublicationRecord.created_at.desc(), LibraryPublicationRecord.id.desc())
        )
        if publication is None:
            self._settle(context, record, None, _ERROR, 'no current publication to bind synced lyrics to')
            return None
        tags = _final_tags(session, publication)
        track_name = tags.get('TITLE', '').strip()
        artist_name = tags.get('ARTIST', '').strip()
        album_name = tags.get('ALBUM', '').strip()
        if not track_name or not artist_name:
            self._settle(context, record, publication, _ERROR, 'published final metadata has no track title and artist')
            return None
        source = session.get(SourceRecord, publication.source_id)
        duration_seconds = None if source is None else _persisted_duration_seconds(source)
        if duration_seconds is None:
            self._settle(context, record, publication, _ERROR, 'published audio duration is unknown')
            return None
        lookup = LrclibLookupRequest(track_name, artist_name, duration_seconds, album_name or None)
        match adapter.lookup(lookup, now=context.now):
            case LrclibSynced(provenance=provenance, synced_lyrics=synced_lyrics):
                self._accept(context, record, publication, provenance, synced_lyrics, duration_seconds)
            case LrclibNoCandidate(provenance=provenance, reason=reason):
                self._settle(context, record, publication, _NO_CANDIDATE, reason, provenance)
            case LrclibProviderError(provenance=provenance, reason=reason):
                self._settle(context, record, publication, _ERROR, reason, provenance)
        return None

    def _accept(
        self,
        context: ExecutionContext,
        record: LibraryRecord,
        publication: LibraryPublicationRecord,
        provenance: LrclibProvenance,
        synced_lyrics: str,
        duration_seconds: int,
    ) -> None:
        try:
            payload = synced_lyrics.encode('utf-8')
        except UnicodeEncodeError:
            self._settle(
                context,
                record,
                publication,
                _VALIDATION_REJECTED,
                _INVALID_UTF8_REASON,
                provenance,
                validation_reason=_INVALID_UTF8_REASON,
            )
            return
        match validate_synced_lyrics(payload, float(duration_seconds)):
            case SyncedLyricsInvalid(reason=reason):
                self._settle(
                    context, record, publication, _VALIDATION_REJECTED, reason, provenance, validation_reason=reason
                )
            case SyncedLyricsValid(text=text):
                written = write_lrc_atomically(LrcWriteRequest(context.config.media_root, Path(publication.path), text))
                match written:
                    case LrcWriteRejected(reason=reason):
                        raise ProcessingInfrastructureError(f'lyric sidecar was not written: {reason}')
                    case LrcWritten() as sidecar:
                        self._settle(
                            context,
                            record,
                            publication,
                            _SYNCED,
                            f'validated synced lyrics written to {sidecar.relative_path}',
                            provenance,
                            sidecar=sidecar,
                        )

    def _skip_disabled(self, context: ExecutionContext, record: LibraryRecord) -> None:
        """Settle a fetch whose provider was switched off after the job was queued, without provider traffic.

        The job must neither reach the provider nor stay queued once the operator disabled it, so the fetch is
        declined with one history event. A record whose lyrics were already validated keeps its state and sidecar
        binding: turning a provider off is not evidence against lyrics it produced earlier. Every other state is
        materialized as "no lyrics were requested" instead of staying pending for a fetch that will never run.
        """
        if record.lyrics_status == _SYNCED:
            return
        self._settle(context, record, None, _NONE, _DISABLED_REASON)

    def _settle(
        self,
        context: ExecutionContext,
        record: LibraryRecord,
        publication: LibraryPublicationRecord | None,
        outcome: str,
        reason: str,
        provenance: LrclibProvenance | None = None,
        *,
        sidecar: LrcWritten | None = None,
        validation_reason: str | None = None,
    ) -> None:
        """Materialize one terminal lyric outcome and its event inside the caller's savepoint.

        The materialized fields move as one unit: a sidecar relative path, publication id, and hash exist only for
        the success outcome, and every other outcome clears them. Sidecar files themselves are never deleted here;
        an existing file is only ever replaced by the atomic write of a later success.
        """
        now = context.now
        publication_id = None if publication is None else publication.id
        record.lyrics_status = outcome
        record.lyrics_path = None if sidecar is None else sidecar.relative_path
        record.lyrics_publication_id = publication_id if sidecar is not None else None
        record.lyrics_sha256 = None if sidecar is None else sidecar.sha256
        record.lyrics_updated_at = now
        record.updated_at = now
        context.session.add(
            LibraryEventRecord(
                library_record_id=record.id,
                source_id=None if publication is None else publication.source_id,
                kind=_EVENT_KINDS[outcome],
                state=outcome,
                reason=reason,
                details_json=_details_json(outcome, publication_id, provenance, sidecar, validation_reason),
                created_at=now,
            )
        )
        context.session.flush()


def _final_tags(session: Session, publication: LibraryPublicationRecord) -> dict[str, str]:
    """Load the final metadata tags of the current publication, without trusting the layer of a stale revision."""
    revision = _final_revision(session, publication)
    return {} if revision is None else _TAGS_ADAPTER.validate_json(revision.tags_json)


def _final_revision(session: Session, publication: LibraryPublicationRecord) -> LibraryMetadataRevisionRecord | None:
    """Resolve the final metadata revision behind one publication, falling back to the newest final revision.

    A publication normally pins the exact final revision it was built from; the newest final revision of the
    record is only a fallback, so a publication written before metadata pinning still resolves usable identity.
    """
    if publication.metadata_revision_id is not None:
        bound = session.get(LibraryMetadataRevisionRecord, publication.metadata_revision_id)
        if bound is not None and bound.layer == 'final':
            return bound
    return session.scalar(
        select(LibraryMetadataRevisionRecord)
        .where(LibraryMetadataRevisionRecord.library_record_id == publication.library_record_id)
        .where(LibraryMetadataRevisionRecord.layer == 'final')
        .order_by(LibraryMetadataRevisionRecord.created_at.desc(), LibraryMetadataRevisionRecord.id.desc())
    )


def _details_json(
    outcome: str,
    publication_id: str | None,
    provenance: LrclibProvenance | None,
    sidecar: LrcWritten | None,
    validation_reason: str | None,
) -> str:
    """Serialize the compact outcome evidence; lyric text is never part of it."""
    details: dict[str, object] = {'outcome': outcome}
    if publication_id is not None:
        details['publication_id'] = publication_id
    if provenance is not None:
        details['provider'] = provenance.provider_name
        details['endpoint'] = provenance.endpoint
        details['request_hash'] = provenance.request_hash
        details['response_sha256'] = provenance.response_sha256
        if provenance.http_status is not None:
            details['http_status'] = provenance.http_status
        if provenance.provider_record_id is not None:
            details['record_id'] = provenance.provider_record_id
    if sidecar is not None:
        details['sidecar_path'] = sidecar.relative_path
        details['sidecar_sha256'] = sidecar.sha256
    if validation_reason is not None:
        details['validation_reason'] = validation_reason
    return json.dumps(details, sort_keys=True, separators=(',', ':'))
