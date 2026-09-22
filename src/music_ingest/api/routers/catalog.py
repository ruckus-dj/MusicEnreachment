from __future__ import annotations

import json
from pathlib import Path
from typing import Annotated, cast

from fastapi import APIRouter, HTTPException, Query
from fastapi.responses import FileResponse, JSONResponse

from music_ingest.api.candidate_views import _display_candidates
from music_ingest.api.catalog_views import _catalog_record_response, _catalog_sort_key
from music_ingest.api.dependencies import SessionFactory
from music_ingest.api.library_access import destination_conflict
from music_ingest.contracts import (
    LibraryAlbumListResponse,
    LibraryAlbumResponse,
    LibraryArtistListResponse,
    LibraryArtistResponse,
    LibraryCatalogQuery,
    LibraryPublicationQuery,
    LibraryRecordListResponse,
    LibraryTrackListResponse,
    LibraryTrackQuery,
    LibraryTrackResponse,
    LyricsStatus,
    ManualActionCountsResponse,
    ManualActionFilter,
    ManualActionListResponse,
)
from music_ingest.models import (
    ReleaseArtworkRecord,
)
from music_ingest.services.library.service import (
    library_active_record_count,
    library_album_tracks,
    library_artist_albums,
    library_artist_names,
    library_manual_action_counts,
    library_manual_action_records,
    library_record_detail,
    library_records,
)


def create_router(session_factory: SessionFactory, *, media_root: Path | None = None) -> APIRouter:
    router = APIRouter()

    @router.get('/api/library/artists', response_model=LibraryArtistListResponse)
    def library_artists(
        query: Annotated[LibraryPublicationQuery, Query()],
    ) -> LibraryArtistListResponse:
        with session_factory() as session:
            return LibraryArtistListResponse(
                items=tuple(
                    LibraryArtistResponse(name=artist.name, track_count=artist.track_count)
                    for artist in library_artist_names(session, query.published)
                ),
                total_track_count=library_active_record_count(session, query.published),
            )

    @router.get('/api/library/albums', response_model=LibraryAlbumListResponse)
    def library_albums(
        query: Annotated[LibraryCatalogQuery, Query()],
    ) -> LibraryAlbumListResponse:
        with session_factory() as session:
            return LibraryAlbumListResponse(
                items=tuple(
                    LibraryAlbumResponse(
                        album_id=album.album_id,
                        album_name=album.album_name,
                        track_count=album.track_count,
                        artwork_url=album.artwork_url,
                    )
                    for album in library_artist_albums(
                        session,
                        None if query.artist_missing else query.artist,
                        published=query.published,
                    )
                )
            )

    @router.get('/api/library/tracks', response_model=LibraryTrackListResponse)
    def library_tracks(
        query: Annotated[LibraryTrackQuery, Query()],
    ) -> LibraryTrackListResponse:
        with session_factory() as session:
            return LibraryTrackListResponse(
                items=tuple(
                    LibraryTrackResponse(
                        record_id=track.record_id,
                        source_id=track.source_id,
                        source_path=track.source_path,
                        artist_name=track.artist_name,
                        album_name=track.album_name,
                        album_id=query.album_id,
                        title=track.title,
                        track_number=track.track_number,
                        source_state=track.source_state,
                        processing_state=track.processing_state,
                        match_state=track.match_state,
                        publication_state=track.publication_state,
                        lyrics_status=cast(LyricsStatus, track.lyrics_status),
                        lyrics_synced=track.lyrics_status == 'synced',
                    )
                    for track in library_album_tracks(
                        session,
                        None if query.artist_missing else query.artist,
                        album_id=query.album_id,
                        album_name=query.album_name,
                        album_missing=query.album_missing,
                        published=query.published,
                    )
                )
            )

    @router.get('/api/library/records', response_model=LibraryRecordListResponse)
    def library_catalog() -> LibraryRecordListResponse:
        with session_factory() as session:
            records = sorted(library_records(session), key=_catalog_sort_key)
            return LibraryRecordListResponse(
                items=tuple(_catalog_record_response(session, record) for record in records)
            )

    @router.get('/api/library/manual-actions', response_model=ManualActionListResponse)
    def manual_actions(
        action: Annotated[ManualActionFilter, Query()] = 'analysis-error',
    ) -> ManualActionListResponse:
        with session_factory() as session:
            analysis_error_count, needs_review_count = library_manual_action_counts(session)
            records = sorted(library_manual_action_records(session, action), key=_catalog_sort_key)
            return ManualActionListResponse(
                items=tuple(_catalog_record_response(session, record) for record in records),
                counts=ManualActionCountsResponse(
                    analysis_error=analysis_error_count,
                    needs_review=needs_review_count,
                ),
            )

    @router.get('/api/library/release-artwork/{release_mbid}')
    def release_artwork(release_mbid: str) -> FileResponse:
        if media_root is None:
            raise HTTPException(status_code=404, detail='artwork not found')
        with session_factory() as session:
            artwork = session.get(ReleaseArtworkRecord, release_mbid)
            if artwork is None or artwork.state != 'ready' or artwork.path is None:
                raise HTTPException(status_code=404, detail='artwork not found')
            path = Path(artwork.path).resolve()
            root = media_root.resolve()
            if path == root or root not in path.parents or not path.is_file():
                raise HTTPException(status_code=404, detail='artwork not found')
            return FileResponse(path, media_type=f'image/{artwork.format_name or "jpeg"}')

    @router.get('/api/library/records/{record_id}')
    def library_catalog_detail(record_id: str) -> JSONResponse:
        try:
            with session_factory() as session:
                record = library_record_detail(session, record_id)
                return JSONResponse(
                    content={
                        'record_id': record.id,
                        'musicbrainz_recording_id': record.musicbrainz_recording_id,
                        'musicbrainz_release_id': record.musicbrainz_release_id,
                        'musicbrainz_artist_id': record.musicbrainz_artist_id,
                        'states': {
                            'source': record.source_state,
                            'processing': record.processing_state,
                            'match': record.match_state,
                            'publication': record.publication_state,
                            'metadata': record.metadata_state,
                        },
                        'lyrics_status': record.lyrics_status,
                        'lyrics_synced': record.lyrics_status == 'synced',
                        'sources': [
                            {
                                'source_id': source.id,
                                'path': source.source_path,
                                'format': source.source_path.rsplit('.', maxsplit=1)[-1],
                                'sha256': source.sha256,
                                'size_bytes': source.size_bytes,
                                'origin': source.origin,
                                'state': source.intake_state,
                                'tag_observations': [
                                    {'name': tag.tag_name, 'value': tag.value, 'format': tag.format_name}
                                    for tag in source.tag_observations
                                ],
                                'fingerprints': [
                                    {
                                        'state': fingerprint.state,
                                        'fingerprint': fingerprint.fingerprint,
                                        'duration_seconds': fingerprint.duration_seconds,
                                        'tool_version': fingerprint.tool_version,
                                    }
                                    for fingerprint in source.fingerprints
                                ],
                                'provider_attempts': [
                                    {
                                        'provider': attempt.provider_name,
                                        'outcome': attempt.outcome,
                                        'snapshot_sha256': attempt.snapshot_sha256,
                                        'created_at': attempt.created_at.isoformat(),
                                    }
                                    for attempt in source.provider_attempts
                                ],
                                'candidates': [
                                    {'candidate_key': candidate_key, 'evidence': evidence.model_dump()}
                                    for candidate_key, evidence in _display_candidates(source)
                                ],
                                'review_decisions': [
                                    {
                                        'state': decision.state,
                                        'rationale': decision.rationale,
                                    }
                                    for decision in source.review_decisions
                                ],
                                'disappeared_at': source.disappeared_at.isoformat()
                                if source.disappeared_at is not None
                                else None,
                            }
                            for source in record.sources
                        ],
                        'publications': [
                            {
                                'publication_id': publication.id,
                                'source_id': publication.source_id,
                                'path': publication.path,
                                'format': publication.format_name,
                                'sha256': publication.content_sha256,
                                'metadata_revision_id': publication.metadata_revision_id,
                                'state': publication.state,
                                'created_at': publication.created_at.isoformat(),
                            }
                            for publication in record.publications
                        ],
                        'metadata_revisions': [
                            {
                                'id': revision.id,
                                'source_id': revision.source_id,
                                'layer': revision.layer,
                                'revision': revision.revision,
                                'tags': json.loads(revision.tags_json),
                                'actor': revision.actor,
                                'created_at': revision.created_at.isoformat(),
                            }
                            for revision in record.metadata_revisions
                        ],
                        'events': [
                            {
                                'id': event.id,
                                'source_id': event.source_id,
                                'kind': event.kind,
                                'state': event.state,
                                'reason': event.reason,
                                'details': json.loads(event.details_json),
                                'created_at': event.created_at.isoformat(),
                            }
                            for event in record.events
                        ],
                        'destination_conflict': next(
                            (
                                conflict
                                for source in record.sources
                                if (conflict := destination_conflict(session, record, source, media_root)) is not None
                            ),
                            None,
                        ),
                    }
                )
        except LookupError as error:
            raise HTTPException(status_code=404, detail='library record not found') from error

    return router
