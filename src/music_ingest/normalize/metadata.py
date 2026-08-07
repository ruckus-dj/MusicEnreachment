from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from subprocess import TimeoutExpired, run
from typing import override

from music_ingest.config.policies import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.normalize.genres import GenreNormalizationError, normalize_genres


class CanonicalSource(StrEnum):
    VERIFIED_RELEASE = 'verified_release'
    AUTOMATIC_FALLBACK = 'automatic_fallback'
    REVIEWED_MANUAL = 'reviewed_manual'
    REVIEWED_LOCAL_ONLY = 'reviewed_local_only'


@dataclass(frozen=True, slots=True)
class CanonicalMetadata:
    source: CanonicalSource
    title: str
    artists: tuple[str, ...]
    album: str
    album_artists: tuple[str, ...]
    date: str
    original_date: str | None
    track_number: int
    track_total: int
    disc_number: int
    disc_total: int
    genres: tuple[str, ...]
    musicbrainz_track_id: str | None
    musicbrainz_album_id: str | None
    musicbrainz_release_group_id: str | None
    isrc: str | None


@dataclass(frozen=True, slots=True)
class MetadataWriteRequest:
    source_path: Path
    output_path: Path
    staging_directory: Path
    metadata: CanonicalMetadata
    fields: FieldPolicy
    genres: GenrePolicy
    metaflac_command: str = 'metaflac'
    timeout_seconds: float = 10.0


@dataclass(frozen=True, slots=True)
class MetadataWriteResult:
    output_path: Path
    tags: tuple[tuple[str, str], ...]


def write_observed_metadata(
    path: Path,
    tags: tuple[tuple[str, str], ...],
    metaflac_command: str = 'metaflac',
    timeout_seconds: float = 10.0,
) -> MetadataWriteResult:
    """Replace a staged file's tags with the observed allowlisted source tags."""
    filtered = tuple((name, value) for name, value in tags if name in ALLOWED_TAG_KEYS)
    _write_tags(path, filtered, metaflac_command, timeout_seconds)
    _verify_tags(path, filtered, metaflac_command, timeout_seconds)
    return MetadataWriteResult(path, filtered)


@dataclass(frozen=True, slots=True)
class MetadataWriteError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def write_canonical_metadata(request: MetadataWriteRequest) -> MetadataWriteResult:
    """Write only canonical Vorbis comments into a new staged FLAC."""
    source_path, output_path, staging_directory = _validate_paths(request)
    tags = _canonical_tags(request.metadata, request.fields, request.genres)
    temporary_path = _copy_to_staging(source_path, staging_directory)
    try:
        _write_tags(temporary_path, tags, request.metaflac_command, request.timeout_seconds)
        _verify_tags(temporary_path, tags, request.metaflac_command, request.timeout_seconds)
        try:
            os.link(temporary_path, output_path)
        except FileExistsError as error:
            raise MetadataWriteError('metadata destination already exists') from error
    finally:
        if temporary_path.exists():
            temporary_path.unlink()
    return MetadataWriteResult(output_path, tags)


def _validate_paths(request: MetadataWriteRequest) -> tuple[Path, Path, Path]:
    source_path = request.source_path.resolve(strict=True)
    output_path = request.output_path.resolve()
    staging_directory = request.staging_directory.resolve(strict=True)
    if not staging_directory.is_dir() or output_path.parent != staging_directory:
        raise MetadataWriteError('metadata output must be directly inside controlled staging')
    if output_path.suffix.casefold() != '.flac':
        raise MetadataWriteError('canonical metadata output must be a FLAC file')
    if source_path == output_path or output_path.exists():
        raise MetadataWriteError('metadata source and destination must be distinct and unused')
    if source_path.stat().st_dev != staging_directory.stat().st_dev:
        raise MetadataWriteError('metadata staging must share the source filesystem')
    return source_path, output_path, staging_directory


def _canonical_tags(
    metadata: CanonicalMetadata, fields: FieldPolicy, genre_policy: GenrePolicy
) -> tuple[tuple[str, str], ...]:
    if frozenset(fields.allowed_tag_keys) != ALLOWED_TAG_KEYS:
        raise MetadataWriteError('field policy does not allow the canonical tag set')
    if not metadata.title or not metadata.album or not metadata.artists or not metadata.album_artists:
        raise MetadataWriteError('canonical title, album, artists, and album artists are required')
    if min(metadata.track_number, metadata.track_total, metadata.disc_number, metadata.disc_total) < 1:
        raise MetadataWriteError('canonical sequence values must be positive')
    if metadata.track_number > metadata.track_total or metadata.disc_number > metadata.disc_total:
        raise MetadataWriteError('canonical sequence values exceed their totals')
    match metadata.source:
        case CanonicalSource.REVIEWED_LOCAL_ONLY:
            identifiers = (
                metadata.musicbrainz_track_id,
                metadata.musicbrainz_album_id,
                metadata.musicbrainz_release_group_id,
            )
            if any(identifiers):
                raise MetadataWriteError('local-only metadata cannot carry MusicBrainz identifiers')
        case CanonicalSource.VERIFIED_RELEASE | CanonicalSource.AUTOMATIC_FALLBACK | CanonicalSource.REVIEWED_MANUAL:
            pass
    try:
        normalized_genres = normalize_genres(metadata.genres, genre_policy)
    except GenreNormalizationError as error:
        raise MetadataWriteError(str(error)) from error
    if not normalized_genres:
        raise MetadataWriteError('at least one approved canonical genre is required')
    separator = fields.list_separator
    required = (
        ('TITLE', metadata.title),
        ('ARTIST', separator.join(metadata.artists)),
        ('ALBUM', metadata.album),
        ('ALBUMARTIST', separator.join(metadata.album_artists)),
        ('DATE', metadata.date),
        ('TRACKNUMBER', str(metadata.track_number)),
        ('TRACKTOTAL', str(metadata.track_total)),
        ('DISCNUMBER', str(metadata.disc_number)),
        ('DISCTOTAL', str(metadata.disc_total)),
        ('GENRE', separator.join(normalized_genres)),
    )
    optional = (
        ('ORIGINALDATE', metadata.original_date),
        ('MUSICBRAINZ_TRACKID', metadata.musicbrainz_track_id),
        ('MUSICBRAINZ_ALBUMID', metadata.musicbrainz_album_id),
        ('MUSICBRAINZ_RELEASEGROUPID', metadata.musicbrainz_release_group_id),
        ('ISRC', metadata.isrc),
    )
    return required + tuple((name, value) for name, value in optional if value is not None)


def _copy_to_staging(source_path: Path, staging_directory: Path) -> Path:
    descriptor, name = tempfile.mkstemp(prefix='.metadata-', suffix='.flac', dir=staging_directory)
    temporary_path = Path(name)
    try:
        with os.fdopen(descriptor, 'wb') as temporary_file, source_path.open('rb') as source_file:
            shutil.copyfileobj(source_file, temporary_file)
            temporary_file.flush()
            os.fsync(temporary_file.fileno())
    except OSError as error:
        temporary_path.unlink(missing_ok=True)
        raise MetadataWriteError('unable to stage FLAC for metadata writing') from error
    return temporary_path


def _write_tags(path: Path, tags: tuple[tuple[str, str], ...], command: str, timeout_seconds: float) -> None:
    arguments = (
        command,
        '--remove-all-tags',
        *(f'--set-tag={name}={value}' for name, value in tags),
        str(path),
    )
    try:
        completed = run(arguments, capture_output=True, check=False, text=True, timeout=timeout_seconds)  # noqa: S603
    except FileNotFoundError as error:
        raise MetadataWriteError('metaflac is unavailable') from error
    except TimeoutExpired as error:
        raise MetadataWriteError('metaflac timed out') from error
    except OSError as error:
        raise MetadataWriteError('metaflac could not execute') from error
    if completed.returncode != 0:
        raise MetadataWriteError('metaflac rejected canonical metadata')


def _verify_tags(path: Path, expected: tuple[tuple[str, str], ...], command: str, timeout_seconds: float) -> None:
    try:
        completed = run(  # noqa: S603
            (command, '--export-tags-to=-', str(path)),
            capture_output=True,
            check=False,
            text=True,
            timeout=timeout_seconds,
        )
    except FileNotFoundError as error:
        raise MetadataWriteError('metaflac is unavailable') from error
    except TimeoutExpired as error:
        raise MetadataWriteError('metaflac timed out') from error
    except OSError as error:
        raise MetadataWriteError('metaflac could not execute') from error
    actual = tuple(tuple(line.split('=', maxsplit=1)) for line in completed.stdout.splitlines() if '=' in line)
    if completed.returncode != 0 or actual != expected:
        raise MetadataWriteError('metaflac did not produce the required canonical tags')
