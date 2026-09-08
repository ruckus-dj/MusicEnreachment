from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

from sqlalchemy import select

from music_ingest.enrichment.artwork import ArtworkWriteError, ManagedArtworkWriteRequest, write_managed_release_artwork
from music_ingest.models import LibraryPublicationRecord, LibraryRecord, ReleaseArtworkRecord
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing.execution import ExecutionContext, ProcessingInfrastructureError


@dataclass(frozen=True, slots=True)
class ArtworkHandler:
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        release_mbid = claimed.job.release_mbid
        if release_mbid is None:
            raise ProcessingInfrastructureError('artwork enrichment requires a release MBID target')
        if not context.settings.artwork_enabled():
            return
        artwork = context.session.get(ReleaseArtworkRecord, release_mbid)
        if artwork is not None and artwork.state == 'ready' and artwork.path is not None:
            if Path(artwork.path).is_file():
                return
            artwork.state, artwork.path, artwork.format_name, artwork.updated_at = 'missing', None, None, context.now
        provider = context.config.artwork_provider
        if provider is None:
            raise ProcessingInfrastructureError('artwork provider is unavailable')
        publication = context.session.scalar(
            select(LibraryPublicationRecord)
            .join(LibraryRecord, LibraryRecord.id == LibraryPublicationRecord.library_record_id)
            .where(LibraryRecord.musicbrainz_release_id == release_mbid)
            .where(LibraryPublicationRecord.state == 'current')
            .order_by(LibraryPublicationRecord.created_at.desc())
        )
        if publication is None:
            raise ProcessingInfrastructureError('published album for artwork release is missing')
        release_directory = Path(publication.path).resolve().parent
        media_root = context.config.media_root.resolve(strict=True)
        if release_directory == media_root or media_root not in release_directory.parents:
            raise ProcessingInfrastructureError('published album is outside the managed media root')
        existing = next(
            (path for path in (release_directory / 'cover.jpg', release_directory / 'cover.webp') if path.is_file()),
            None,
        )
        if existing is not None:
            _save_artwork_state(context, artwork, release_mbid, existing)
            return
        candidate = provider.fetch_artwork(release_mbid)
        if candidate is None:
            raise ProcessingInfrastructureError('artwork provider returned no cover')
        try:
            output = write_managed_release_artwork(
                ManagedArtworkWriteRequest(context.config.media_root, release_directory, release_mbid, candidate)
            )
        except ArtworkWriteError as error:
            if str(error) != 'release already has artwork':
                raise
            output = next(
                (
                    path
                    for path in (release_directory / 'cover.jpg', release_directory / 'cover.webp')
                    if path.is_file()
                ),
                None,
            )
            if output is None:
                raise
        _save_artwork_state(context, artwork, release_mbid, output)


def _save_artwork_state(
    context: ExecutionContext, artwork: ReleaseArtworkRecord | None, release_mbid: str, output: Path
) -> None:
    if artwork is None:
        context.session.add(
            ReleaseArtworkRecord(
                release_mbid=release_mbid,
                path=str(output),
                format_name=output.suffix.removeprefix('.'),
                provider='musicbrainz',
                state='ready',
                created_at=context.now,
                updated_at=context.now,
            )
        )
    else:
        artwork.path, artwork.format_name, artwork.provider, artwork.state, artwork.updated_at = (
            str(output),
            output.suffix.removeprefix('.'),
            'musicbrainz',
            'ready',
            context.now,
        )
    context.session.flush()
