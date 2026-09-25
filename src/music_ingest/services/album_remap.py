from __future__ import annotations

import hashlib
import json
from collections.abc import Iterable
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Final, override

from pydantic import TypeAdapter
from sqlalchemy import select, text
from sqlalchemy.orm import Session, noload, selectinload

from music_ingest.contracts import (
    AlbumReleaseSummary,
    AlbumRemapApplyRequest,
    AlbumRemapApplyResponse,
    AlbumRemapContextResponse,
    AlbumRemapSelector,
    AlbumRemapSource,
    AlbumRemapTrackSlot,
)
from music_ingest.contracts.api import ArtistCredit, Release, Track
from music_ingest.models import LibraryMetadataRevisionRecord, LibraryRecord, SourceRecord
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.association import ManualAssociationRequest, RecordingAssociationService
from music_ingest.services.library.service import (
    AlbumTrackSelector,
    album_track_selector_matches,
    append_metadata_revision,
    library_album_tracks,
)

_TAGS_ADAPTER = TypeAdapter(dict[str, str])
_POSTGRESQL_DIALECT: Final = 'postgresql'
_SELECTOR_LOCK_NAMESPACE: Final = b'music-ingest.album-remap.selector.v1\0'


@dataclass(frozen=True, slots=True)
class AlbumRemapConflict(Exception):
    detail: str

    @override
    def __str__(self) -> str:
        return self.detail


@dataclass(frozen=True, slots=True)
class AlbumRemapRelease:
    release: Release
    snapshot_token: str


def load_context(session: Session, selector: AlbumRemapSelector) -> AlbumRemapContextResponse:
    return _context(_matching_sources(session, selector))


def release_summary(release: Release) -> AlbumReleaseSummary:
    return AlbumReleaseSummary(
        release_mbid=release.id,
        title=release.title,
        artist_credit=_artist_credit(release.artist_credit),
        date=release.date,
        country=release.country,
    )


def preview_slots(context: AlbumRemapContextResponse, release: Release) -> tuple[AlbumRemapTrackSlot, ...]:
    ordinary = tuple(
        _slot(track, medium.position or 1, True)
        for medium in release.media
        for track in ((medium.pregap,) if medium.pregap is not None else ()) + medium.tracks
    )
    data = tuple(_slot(track, medium.position or 1, False) for medium in release.media for track in medium.data_tracks)
    return _with_suggestions(context.sources, release.id, ordinary + data)


def apply_remap(
    session: Session, request: AlbumRemapApplyRequest, selected: AlbumRemapRelease
) -> AlbumRemapApplyResponse:
    context = _locked_context(session, request.selector)
    if context.album_snapshot_token != request.album_snapshot_token:
        raise AlbumRemapConflict('album snapshot is stale')
    if not request.assignments:
        raise AlbumRemapConflict('album remap requires at least one assignment')
    slots = preview_slots(context, selected.release)
    source_ids = {source.source_id for source in context.sources}
    assignment_ids = tuple(assignment.source_id for assignment in request.assignments)
    unmatched_ids = request.unmatched_source_ids
    if len(set(assignment_ids)) != len(assignment_ids) or len(set(unmatched_ids)) != len(unmatched_ids):
        raise AlbumRemapConflict('source assignments must not contain duplicates')
    if set(assignment_ids).intersection(unmatched_ids) or set(assignment_ids).union(unmatched_ids) != source_ids:
        raise AlbumRemapConflict('assignments and unmatched sources must partition the album')
    assignable = {slot.track_mbid: slot for slot in slots if slot.assignable}
    requested_slots = tuple(assignable.get(str(assignment.track_mbid)) for assignment in request.assignments)
    if None in requested_slots:
        raise AlbumRemapConflict('assignment references an invalid or data track slot')
    assigned_slots = tuple(slot for slot in requested_slots if slot is not None)
    if len({slot.recording_mbid for slot in assigned_slots}) != len(assigned_slots):
        raise AlbumRemapConflict('assignments cannot select distinct slots with one recording identity')
    source_by_id = {source.source_id: source for source in context.sources}
    now = datetime.now(UTC)
    publication_refresh_queued = False
    for assignment in sorted(request.assignments, key=lambda item: item.source_id):
        slot = assignable[str(assignment.track_mbid)]
        source = source_by_id[assignment.source_id]
        result = RecordingAssociationService(session).associate_verified_manual(
            ManualAssociationRequest(
                source.source_id,
                slot.recording_mbid,
                now,
                selected.release.id,
                'manual_album_remap',
                slot.track_mbid,
            )
        )
        publication_refresh_queued = result.publication_refresh_queued or publication_refresh_queued
        tags = {**source.canonical_tags, **_selected_tags(selected.release, slot)}
        _ = append_metadata_revision(
            session, result.library_record_id, source.source_id, 'final', tags, 'manual_album_remap', now
        )
    artwork = JobRepository(session).enqueue_release_artwork(selected.release.id, now)
    return AlbumRemapApplyResponse(
        assigned_source_ids=tuple(sorted(assignment_ids)),
        unmatched_source_ids=tuple(sorted(unmatched_ids)),
        publication_refresh_queued=publication_refresh_queued,
        queued_release_artwork=artwork is not None,
    )


def _matching_sources(session: Session, selector: AlbumRemapSelector) -> tuple[SourceRecord, ...]:
    sources = _sources(session, selector, locked=False)
    return tuple(source for source in sources if _matches_selector(source, selector))


def _matches_selector(source: SourceRecord, selector: AlbumRemapSelector) -> bool:
    record = _record(source)
    return album_track_selector_matches(
        AlbumTrackSelector(
            selector.artist_name,
            selector.artist_missing,
            None if selector.release_mbid is None else str(selector.release_mbid),
            selector.album_name,
            selector.album_missing,
        ),
        record.musicbrainz_release_id,
        _tags(source),
    )


def _locked_context(session: Session, selector: AlbumRemapSelector) -> AlbumRemapContextResponse:
    _acquire_selector_lock(session, selector)
    sources = _sources(session, selector, locked=True)
    record_ids = tuple(sorted({source.library_record_id for source in sources if source.library_record_id is not None}))
    _ = tuple(
        session.scalars(
            select(LibraryRecord)
            .where(LibraryRecord.id.in_(record_ids))
            .order_by(LibraryRecord.id)
            .options(selectinload(LibraryRecord.metadata_revisions))
            .execution_options(populate_existing=True)
            .with_for_update()
        ).all()
    )
    return _context(tuple(source for source in sources if _matches_selector(source, selector)))


def _sources(session: Session, selector: AlbumRemapSelector, *, locked: bool) -> tuple[SourceRecord, ...]:
    source_ids = tuple(
        track.source_id
        for track in library_album_tracks(
            session,
            selector.artist_name,
            artist_missing=selector.artist_missing,
            album_id=None if selector.release_mbid is None else str(selector.release_mbid),
            album_name=selector.album_name,
            album_missing=selector.album_missing,
        )
    )
    if not source_ids:
        return ()
    query = (
        select(SourceRecord)
        .join(SourceRecord.library_record)
        .where(SourceRecord.id.in_(source_ids), SourceRecord.disappeared_at.is_(None))
        .options(
            noload('*'),
            selectinload(SourceRecord.tag_observations),
            selectinload(SourceRecord.fingerprints),
            selectinload(SourceRecord.library_record).selectinload(LibraryRecord.metadata_revisions),
        )
        .order_by(SourceRecord.id)
    )
    if locked:
        query = query.with_for_update()
    return tuple(session.scalars(query).all())


def _context(sources: tuple[SourceRecord, ...]) -> AlbumRemapContextResponse:
    responses = tuple(_source_response(source) for source in sources)
    return AlbumRemapContextResponse(album_snapshot_token=_token(sources), sources=responses)


def _acquire_selector_lock(session: Session, selector: AlbumRemapSelector) -> None:
    if session.get_bind().dialect.name != _POSTGRESQL_DIALECT:
        return
    lock_key = int.from_bytes(
        hashlib.sha256(_SELECTOR_LOCK_NAMESPACE + selector.model_dump_json().encode()).digest()[:8],
        byteorder='big',
        signed=True,
    )
    _ = session.execute(text('SELECT pg_advisory_xact_lock(:lock_key)'), {'lock_key': lock_key})


def _source_response(source: SourceRecord) -> AlbumRemapSource:
    record = _record(source)
    duration_seconds = source.duration_seconds
    if duration_seconds is None and source.fingerprints:
        fingerprint_duration = source.fingerprints[-1].duration_seconds
        duration_seconds = None if fingerprint_duration is None else round(fingerprint_duration)
    return AlbumRemapSource(
        source_id=source.id,
        record_id=record.id,
        path=source.source_path,
        sha256=source.sha256,
        duration_seconds=duration_seconds,
        source_metadata_revision=source.source_metadata_revision,
        recording_mbid=record.musicbrainz_recording_id,
        release_mbid=record.musicbrainz_release_id,
        canonical_tags=_tags(source),
    )


def _tags(source: SourceRecord) -> dict[str, str]:
    revision = _canonical_revision(source)
    if revision is not None:
        return _TAGS_ADAPTER.validate_json(revision.tags_json)
    values: dict[str, list[str]] = {}
    for observation in source.tag_observations:
        if observation.selected is not False:
            values.setdefault(observation.tag_name, []).append(observation.value)
    return {name: '; '.join(items) for name, items in values.items()}


def _canonical_revision(source: SourceRecord) -> LibraryMetadataRevisionRecord | None:
    record = _record(source)
    for layer in ('final', 'original'):
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == source.id and item.layer == layer
            ),
            None,
        )
        if revision is not None:
            return revision
    return None


def _record(source: SourceRecord) -> LibraryRecord:
    if source.library_record is None:
        raise LookupError(source.id)
    return source.library_record


def _slot(track: Track, medium_position: int, assignable: bool) -> AlbumRemapTrackSlot:
    if track.id is None:
        raise AlbumRemapConflict('release track is missing a MusicBrainz track identity')
    return AlbumRemapTrackSlot(
        track_mbid=track.id,
        recording_mbid=track.recording.id,
        medium_position=medium_position,
        track_position=track.position,
        title=track.title,
        artist_credit=_artist_credit(track.recording.artist_credit),
        duration_seconds=None if track.length is None else track.length / 1000,
        assignable=assignable,
    )


def _with_suggestions(
    sources: tuple[AlbumRemapSource, ...], release_mbid: str, slots: tuple[AlbumRemapTrackSlot, ...]
) -> tuple[AlbumRemapTrackSlot, ...]:
    ordinary = tuple(slot for slot in slots if slot.assignable)
    recording_slots = _unique_slots((slot.recording_mbid, slot) for slot in ordinary)
    position_slots = _unique_slots((f'{slot.medium_position}:{slot.track_position}', slot) for slot in ordinary)
    suggestions: dict[str, str] = {}
    for source in sources:
        slot = None
        if source.release_mbid == release_mbid and source.recording_mbid is not None:
            slot = recording_slots.get(source.recording_mbid)
        if slot is None:
            position = _source_position(source.canonical_tags)
            if position is not None:
                slot = position_slots.get(position)
        if slot is not None and slot.track_mbid not in suggestions:
            suggestions[slot.track_mbid] = source.source_id
    return tuple(slot.model_copy(update={'suggested_source_id': suggestions.get(slot.track_mbid)}) for slot in slots)


def _unique_slots(items: Iterable[tuple[str | None, AlbumRemapTrackSlot]]) -> dict[str, AlbumRemapTrackSlot]:
    values: dict[str, AlbumRemapTrackSlot | None] = {}
    for key, slot in items:
        if key is not None:
            values[key] = slot if key not in values else None
    return {key: slot for key, slot in values.items() if slot is not None}


def _source_position(tags: dict[str, str]) -> str | None:
    track = tags.get('TRACKNUMBER')
    if track is None:
        return None
    disc = tags.get('DISCNUMBER', '1').split('/', 1)[0]
    return f'{disc}:{track.split("/", 1)[0]}'


def _selected_tags(release: Release, slot: AlbumRemapTrackSlot) -> dict[str, str]:
    return {
        'TITLE': slot.title,
        'ARTIST': slot.artist_credit or _artist_credit(release.artist_credit),
        'ALBUM': release.title,
        'TRACKNUMBER': str(slot.track_position),
        'DISCNUMBER': str(slot.medium_position),
        'MUSICBRAINZ_TRACKID': slot.track_mbid,
        'MUSICBRAINZ_RECORDINGID': slot.recording_mbid,
        'MUSICBRAINZ_ALBUMID': release.id,
    }


def _artist_credit(credits: tuple[ArtistCredit, ...]) -> str:
    return ''.join(f'{credit.name}{credit.joinphrase}' for credit in credits)


def _token(sources: tuple[SourceRecord, ...]) -> str:
    serialized = '\n'.join(
        json.dumps(
            {
                'source_id': source.id,
                'record_id': _record(source).id,
                'sha256': source.sha256,
                'source_metadata_revision': source.source_metadata_revision,
                'recording_mbid': _record(source).musicbrainz_recording_id,
                'release_mbid': _record(source).musicbrainz_release_id,
                'canonical_revision_id': None if (revision := _canonical_revision(source)) is None else revision.id,
                'canonical_tags': _tags(source),
            },
            sort_keys=True,
            separators=(',', ':'),
        )
        for source in sources
    )
    return hashlib.sha256(serialized.encode()).hexdigest()
