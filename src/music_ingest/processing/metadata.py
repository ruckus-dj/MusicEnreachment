from __future__ import annotations

import re
from hashlib import sha256
from pathlib import Path
from subprocess import run

from music_ingest.config.policies import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.metadata import CanonicalMetadata, CanonicalSource


def read_tags(path: Path, command: str, timeout_seconds: float) -> tuple[tuple[str, str], ...]:
    completed = run(  # noqa: S603
        (command, '--export-tags-to=-', str(path)), capture_output=True, check=False, text=True, timeout=timeout_seconds
    )
    if completed.returncode != 0:
        raise ValueError('metaflac could not read source tags')
    return tuple(
        (parts[0], parts[1])
        for line in completed.stdout.splitlines()
        if (parts := line.split('=', maxsplit=1)) and len(parts) == 2
    )


def fallback_metadata(
    tags: tuple[tuple[str, str], ...], source: CanonicalSource = CanonicalSource.AUTOMATIC_FALLBACK
) -> CanonicalMetadata | None:
    values = {name: value for name, value in tags}
    title = values.get('TITLE')
    artist = values.get('ARTIST')
    album = values.get('ALBUM')
    album_artist = values.get('ALBUMARTIST')
    date = values.get('DATE')
    track_number = values.get('TRACKNUMBER')
    track_total = values.get('TRACKTOTAL')
    disc_number = values.get('DISCNUMBER')
    disc_total = values.get('DISCTOTAL')
    genre = values.get('GENRE')
    if (
        title is None
        or artist is None
        or album is None
        or album_artist is None
        or date is None
        or track_number is None
        or track_total is None
        or disc_number is None
        or disc_total is None
        or genre is None
    ):
        return None
    genres = tuple(filter(None, genre.split('; ')))
    if not genres:
        return None
    return CanonicalMetadata(
        source,
        title,
        tuple(artist.split('; ')),
        album,
        tuple(album_artist.split('; ')),
        date,
        values.get('ORIGINALDATE'),
        int(track_number),
        int(track_total),
        int(disc_number),
        int(disc_total),
        genres,
        values.get('MUSICBRAINZ_TRACKID'),
        values.get('MUSICBRAINZ_ALBUMID'),
        values.get('MUSICBRAINZ_RELEASEGROUPID'),
        values.get('ISRC'),
    )


def publication_layout(tags: tuple[tuple[str, str], ...], source_name: str) -> tuple[str, str]:
    """Return the stable media directory and filename for one source track."""
    metadata = fallback_metadata(tags)
    if metadata is None:
        return 'Unsorted', _safe_component(Path(source_name).name, 'track.flac')
    artist = _safe_component(' & '.join(metadata.album_artists), 'Unknown Artist')
    album = _safe_component(metadata.album, 'Unknown Album')
    title = _safe_component(metadata.title, 'Unknown Track')
    prefix = (
        f'{metadata.disc_number:02d}-{metadata.track_number:02d}'
        if metadata.disc_total > 1
        else f'{metadata.track_number:02d}'
    )
    filename = f'{prefix} - {title}.flac'
    return f'{artist}/{album}', filename


def _safe_component(value: str, fallback: str) -> str:
    cleaned = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '-', value)
    cleaned = re.sub(r'\s*-\s*', ' - ', cleaned).strip(' .')
    return cleaned or fallback


def field_policy() -> FieldPolicy:
    return FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=tuple(sorted(ALLOWED_TAG_KEYS)))


def genre_policy(fallback_genres: tuple[str, ...]) -> GenrePolicy:
    canonical_genres = tuple(dict.fromkeys(('Hip Hop', 'Alternative Rock', *fallback_genres)))
    return GenrePolicy(
        schema_version=1,
        canonical_genres=canonical_genres,
        aliases={'hip hop': ('Hip Hop',), 'alternative rock': ('Alternative Rock',)},
    )


def file_hash(path: Path) -> str:
    digest = sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


_read_tags = read_tags
_fallback_metadata = fallback_metadata
_publication_layout = publication_layout
_field_policy = field_policy
_genre_policy = genre_policy
_hash = file_hash
