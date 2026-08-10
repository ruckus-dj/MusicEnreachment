from __future__ import annotations

import re
from hashlib import sha256
from pathlib import Path
from subprocess import run

from music_ingest.dto import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.metadata import CanonicalMetadata, CanonicalSource
from music_ingest.settings import RuntimeSettings


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
    values = {name: value for name, value in tags}
    suffix = Path(source_name).suffix.casefold() or '.flac'
    if metadata is None:
        artist = values.get('ALBUMARTIST') or values.get('ARTIST')
        album = values.get('ALBUM')
        title = values.get('TITLE')
        track_number = _positive_int(values.get('TRACKNUMBER'))
        disc_number = _positive_int(values.get('DISCNUMBER'))
        disc_total = _positive_int(values.get('DISCTOTAL'))
        if not artist or not album:
            return 'Unsorted', f'Track 01{suffix}'
        safe_artist = _safe_component(artist, 'Unknown Artist')
        safe_album = _safe_component(album, 'Unknown Album')
        if not title:
            filename = f'Track {track_number:02d}{suffix}' if track_number is not None else f'Track 01{suffix}'
        else:
            safe_title = _safe_component(title, 'Unknown Track')
            prefix = _track_prefix(track_number, disc_number, disc_total)
            filename = f'{prefix} - {safe_title}{suffix}' if prefix else f'{safe_title}{suffix}'
        return f'{safe_artist}/{safe_album}', filename
    artist = _safe_component(' & '.join(metadata.album_artists), 'Unknown Artist')
    album = _safe_component(metadata.album, 'Unknown Album')
    title = _safe_component(metadata.title, 'Unknown Track')
    prefix = _track_prefix(metadata.track_number, metadata.disc_number, metadata.disc_total)
    return f'{artist}/{album}', f'{prefix} - {title}{suffix}'


def _positive_int(value: str | None) -> int | None:
    try:
        parsed = int(value) if value is not None else None
    except ValueError:
        return None
    return parsed if parsed is not None and parsed > 0 else None


def _track_prefix(track_number: int | None, disc_number: int | None, disc_total: int | None) -> str:
    if track_number is None:
        return ''
    if disc_total is not None and disc_total > 1 and disc_number:
        return f'{disc_number:02d}-{track_number:02d}'
    return f'{track_number:02d}'


def next_unsorted_filename(directory: Path, suffix: str) -> str:
    """Return the lowest unused Track NN name across supported audio formats."""
    used_numbers = {
        int(match.group(1))
        for path in directory.glob('Track *')
        if (match := re.fullmatch(r'Track (\d{2})\.[^.]+', path.name)) is not None
    }
    number = 1
    while number in used_numbers:
        number += 1
    return f'Track {number:02d}{suffix}'


def _safe_component(value: str, fallback: str) -> str:
    cleaned = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '-', value)
    cleaned = re.sub(r'\s*-\s*', ' - ', cleaned).strip(' .')
    return cleaned or fallback


def field_policy() -> FieldPolicy:
    return FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=tuple(sorted(ALLOWED_TAG_KEYS)))


def genre_policy(fallback_genres: tuple[str, ...], settings: RuntimeSettings | None = None) -> GenrePolicy:
    configured_genres = () if settings is None else settings.canonical_genres
    canonical_genres = tuple(dict.fromkeys((*configured_genres, *fallback_genres)))
    return GenrePolicy(
        schema_version=1,
        canonical_genres=canonical_genres,
        aliases={} if settings is None else settings.genre_aliases,
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
