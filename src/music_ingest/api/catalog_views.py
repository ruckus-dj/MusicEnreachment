from __future__ import annotations

import json
from pathlib import Path

from sqlalchemy.orm import Session

from music_ingest.dto import (
    LibraryRecordSummaryResponse,
)
from music_ingest.models import (
    LibraryRecord,
    ReleaseArtworkRecord,
)


def _catalog_tags(record: LibraryRecord, source_id: str) -> dict[str, str]:
    for layer in ('final', 'original'):
        revision = next(
            (
                item
                for item in reversed(record.metadata_revisions)
                if item.source_id == source_id and item.layer == layer
            ),
            None,
        )
        if revision is not None:
            return json.loads(revision.tags_json)
    source = next(item for item in record.sources if item.id == source_id)
    return {tag.tag_name: tag.value for tag in source.tag_observations}


def _track_number(value: str | None) -> int | None:
    if value is None:
        return None
    try:
        return int(value.split('/', maxsplit=1)[0].strip())
    except ValueError:
        return None


def _catalog_sort_key(record: LibraryRecord) -> tuple[str, str, int, str, str, str]:
    if not record.sources:
        return ('', '', 2**31 - 1, '', record.id, '')
    source = record.sources[0]
    tags = _catalog_tags(record, source.id)
    track_number = _track_number(tags.get('TRACKNUMBER'))
    return (
        tags.get('ARTIST', 'Неизвестный исполнитель').strip().casefold(),
        tags.get('ALBUM', 'Без альбома').strip().casefold(),
        track_number if track_number is not None else 2**31 - 1,
        tags.get('TITLE', '').strip().casefold(),
        record.id,
        source.id,
    )


def _catalog_record_response(session: Session, record: LibraryRecord) -> LibraryRecordSummaryResponse:
    return LibraryRecordSummaryResponse.model_validate(
        {
            'record_id': record.id,
            'musicbrainz_recording_id': record.musicbrainz_recording_id,
            'musicbrainz_release_id': record.musicbrainz_release_id,
            'musicbrainz_artist_id': record.musicbrainz_artist_id,
            'artwork': (
                {
                    'url': f'/api/library/release-artwork/{record.musicbrainz_release_id}',
                    'state': artwork.state,
                }
                if record.musicbrainz_release_id is not None
                and (artwork := session.get(ReleaseArtworkRecord, record.musicbrainz_release_id)) is not None
                and artwork.state == 'ready'
                and artwork.path is not None
                and Path(artwork.path).is_file()
                else None
            ),
            'source_state': record.source_state,
            'processing_state': record.processing_state,
            'match_state': record.match_state,
            'publication_state': record.publication_state,
            'metadata_state': record.metadata_state,
            'metadata_revisions': [
                {
                    'source_id': revision.source_id,
                    'layer': revision.layer,
                    'revision': revision.revision,
                    'tags': json.loads(revision.tags_json),
                }
                for revision in record.metadata_revisions
            ],
            'sources': [
                {
                    'source_id': source.id,
                    'path': source.source_path,
                    'format': source.source_path.rsplit('.', maxsplit=1)[-1],
                    'sha256': source.sha256,
                    'state': source.intake_state,
                    'tag_observations': [
                        {'name': tag.tag_name, 'value': tag.value, 'format': tag.format_name}
                        for tag in source.tag_observations
                    ],
                    'disappeared_at': source.disappeared_at.isoformat() if source.disappeared_at is not None else None,
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
                    'state': publication.state,
                    'created_at': publication.created_at.isoformat(),
                }
                for publication in record.publications
            ],
        }
    )
