from __future__ import annotations

from hashlib import sha256
from pathlib import Path
from subprocess import run

from music_ingest.config.policies import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.metadata import CanonicalMetadata, CanonicalSource


def _read_tags(path: Path, command: str, timeout_seconds: float) -> tuple[tuple[str, str], ...]:
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


def _fallback_metadata(tags: tuple[tuple[str, str], ...]) -> CanonicalMetadata | None:
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
        CanonicalSource.AUTOMATIC_FALLBACK,
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
        None,
        None,
        None,
        values.get('ISRC'),
    )


def _field_policy() -> FieldPolicy:
    return FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=tuple(sorted(ALLOWED_TAG_KEYS)))


def _genre_policy(fallback_genres: tuple[str, ...]) -> GenrePolicy:
    canonical_genres = tuple(dict.fromkeys(('Hip Hop', 'Alternative Rock', *fallback_genres)))
    return GenrePolicy(
        schema_version=1,
        canonical_genres=canonical_genres,
        aliases={'hip hop': ('Hip Hop',), 'alternative rock': ('Alternative Rock',)},
    )


def _hash(path: Path) -> str:
    digest = sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()
