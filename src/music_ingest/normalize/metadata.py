from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import override

from mutagen import MutagenError
from mutagen.flac import FLAC
from mutagen.id3 import ID3, TALB, TCON, TDOR, TDRC, TIT2, TPE1, TPE2, TPOS, TRCK, TSRC, UFID
from mutagen.mp4 import MP4
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis

from music_ingest.dto import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.normalize.genres import GenreNormalizationError, normalize_genres
from music_ingest.normalize.tags import MetadataTagError, write_normalized_tags


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
    performer: str | None = None


@dataclass(frozen=True, slots=True)
class MetadataWriteRequest:
    source_path: Path
    output_path: Path
    staging_directory: Path
    metadata: CanonicalMetadata
    fields: FieldPolicy
    genres: GenrePolicy


@dataclass(frozen=True, slots=True)
class MetadataWriteResult:
    output_path: Path
    tags: tuple[tuple[str, str], ...]


def write_observed_metadata(
    path: Path,
    tags: tuple[tuple[str, str], ...],
) -> MetadataWriteResult:
    """Replace a staged file's tags with the observed allowlisted source tags."""
    filtered = tuple((name, value) for name, value in tags if name in ALLOWED_TAG_KEYS)
    try:
        _ = write_normalized_tags(path, filtered)
    except MetadataTagError as error:
        raise MetadataWriteError('Mutagen rejected observed metadata') from error
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
    temporary_path = _copy_to_staging(source_path, staging_directory, output_path.suffix)
    try:
        if output_path.suffix.casefold() == '.mp3':
            _write_mp3_tags(temporary_path, request.metadata)
            _verify_mp3_tags(temporary_path, request.metadata)
        elif output_path.suffix.casefold() in {'.m4a', '.mp4'}:
            _write_mp4_tags(temporary_path, request.metadata)
            _verify_mp4_tags(temporary_path, request.metadata)
        else:
            _write_vorbis_comments(temporary_path, tags, output_path.suffix)
            _verify_vorbis_comments(temporary_path, tags, output_path.suffix)
        try:
            shutil.copy2(temporary_path, output_path)
        except FileExistsError as error:
            raise MetadataWriteError('metadata destination already exists') from error
    finally:
        if temporary_path.exists():
            temporary_path.unlink()
    return MetadataWriteResult(output_path, tags)


def _write_vorbis_comments(path: Path, tags: tuple[tuple[str, str], ...], suffix: str) -> None:
    try:
        audio = _open_vorbis_comments(path, suffix)
        audio.clear()
        for name, value in tags:
            audio[name] = [value]
        audio.save()
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen rejected canonical Vorbis comments') from error


def _verify_vorbis_comments(path: Path, expected: tuple[tuple[str, str], ...], suffix: str) -> None:
    try:
        audio = _open_vorbis_comments(path, suffix)
        actual = tuple((name.upper(), values[0]) for name, values in audio.items() if values)
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen could not reopen canonical Vorbis comments') from error
    if frozenset(actual) != frozenset(expected):
        raise MetadataWriteError('Mutagen did not produce the required canonical tags')


def _open_vorbis_comments(path: Path, suffix: str) -> FLAC | OggVorbis | OggOpus:
    match suffix.casefold():
        case '.flac':
            return FLAC(path)
        case '.ogg':
            return OggVorbis(path)
        case '.opus':
            return OggOpus(path)
        case unreachable:
            raise MetadataWriteError(f'unsupported Vorbis-comment suffix: {unreachable}')


def _write_mp3_tags(path: Path, metadata: CanonicalMetadata) -> None:
    try:
        tags = ID3()
        tags.add(TIT2(encoding=3, text=metadata.title))
        tags.add(TPE1(encoding=3, text=list(metadata.artists)))
        tags.add(TPE2(encoding=3, text=list(metadata.album_artists)))
        tags.add(TALB(encoding=3, text=metadata.album))
        tags.add(TRCK(encoding=3, text=f'{metadata.track_number}/{metadata.track_total}'))
        tags.add(TPOS(encoding=3, text=f'{metadata.disc_number}/{metadata.disc_total}'))
        tags.add(TDRC(encoding=3, text=metadata.date))
        if metadata.original_date is not None:
            tags.add(TDOR(encoding=3, text=metadata.original_date))
        tags.add(TCON(encoding=3, text=list(metadata.genres)))
        if metadata.isrc is not None:
            tags.add(TSRC(encoding=3, text=metadata.isrc))
        if metadata.musicbrainz_track_id is not None:
            tags.add(UFID(owner='musicbrainz.org', data=metadata.musicbrainz_track_id.encode()))
        tags.save(path)
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen rejected canonical MP3 metadata') from error


def _verify_mp3_tags(path: Path, metadata: CanonicalMetadata) -> None:
    try:
        tags = ID3(path)
        expected = {
            'TIT2': metadata.title,
            'TPE1': tuple(metadata.artists),
            'TPE2': tuple(metadata.album_artists),
            'TALB': metadata.album,
            'TRCK': f'{metadata.track_number}/{metadata.track_total}',
            'TPOS': f'{metadata.disc_number}/{metadata.disc_total}',
            'TDRC': metadata.date,
            'TCON': tuple(metadata.genres),
        }
        actual = {
            'TIT2': tags['TIT2'].text[0],
            'TPE1': tuple(tags['TPE1'].text),
            'TPE2': tuple(tags['TPE2'].text),
            'TALB': tags['TALB'].text[0],
            'TRCK': tags['TRCK'].text[0],
            'TPOS': tags['TPOS'].text[0],
            'TDRC': str(tags['TDRC'].text[0]),
            'TCON': tuple(tags['TCON'].text),
        }
        if metadata.original_date is not None:
            expected['TDOR'] = metadata.original_date
            actual['TDOR'] = str(tags['TDOR'].text[0])
        if metadata.isrc is not None:
            expected['TSRC'] = metadata.isrc
            actual['TSRC'] = tags['TSRC'].text[0]
        ufid = tags.getall('UFID:musicbrainz.org')
        if metadata.musicbrainz_track_id is not None and (
            not ufid or ufid[0].data.decode() != metadata.musicbrainz_track_id
        ):
            raise MetadataWriteError('Mutagen did not produce the required MusicBrainz UFID')
    except (MutagenError, OSError, KeyError) as error:
        raise MetadataWriteError('Mutagen could not reopen canonical MP3 metadata') from error
    if any(value is not None and actual[key] != value for key, value in expected.items()):
        raise MetadataWriteError('Mutagen did not produce the required canonical MP3 frames')


def _write_mp4_tags(path: Path, metadata: CanonicalMetadata) -> None:
    try:
        audio = MP4(path)
        audio.clear()
        audio['©nam'] = [metadata.title]
        audio['©ART'] = list(metadata.artists)
        audio['aART'] = list(metadata.album_artists)
        audio['©alb'] = [metadata.album]
        audio['trkn'] = [(metadata.track_number, metadata.track_total)]
        audio['disk'] = [(metadata.disc_number, metadata.disc_total)]
        audio['©day'] = [metadata.date]
        audio['©gen'] = ['; '.join(metadata.genres)]
        if metadata.isrc is not None:
            audio['----:com.apple.iTunes:ISRC'] = [metadata.isrc.encode()]
        if metadata.musicbrainz_track_id is not None:
            audio['----:com.apple.iTunes:MusicBrainz Track Id'] = [metadata.musicbrainz_track_id.encode()]
        audio.save()
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen rejected canonical MP4 metadata') from error


def _verify_mp4_tags(path: Path, metadata: CanonicalMetadata) -> None:
    try:
        tags = MP4(path).tags
        if tags is None:
            raise MetadataWriteError('Mutagen could not reopen canonical MP4 metadata')
        expected = {
            '©nam': [metadata.title],
            '©ART': list(metadata.artists),
            'aART': list(metadata.album_artists),
            '©alb': [metadata.album],
            'trkn': [(metadata.track_number, metadata.track_total)],
            'disk': [(metadata.disc_number, metadata.disc_total)],
            '©day': [metadata.date],
            '©gen': ['; '.join(metadata.genres)],
            '----:com.apple.iTunes:ISRC': [metadata.isrc.encode()] if metadata.isrc is not None else None,
            '----:com.apple.iTunes:MusicBrainz Track Id': (
                [metadata.musicbrainz_track_id.encode()] if metadata.musicbrainz_track_id is not None else None
            ),
        }
        actual = {name: tags.get(name) for name, value in expected.items() if value is not None}
    except (MutagenError, OSError, KeyError) as error:
        raise MetadataWriteError('Mutagen could not reopen canonical MP4 metadata') from error
    if actual != {name: value for name, value in expected.items() if value is not None}:
        raise MetadataWriteError('Mutagen did not produce the required canonical MP4 atoms')


def _validate_paths(request: MetadataWriteRequest) -> tuple[Path, Path, Path]:
    source_path = request.source_path.resolve(strict=True)
    output_path = request.output_path.resolve()
    staging_directory = request.staging_directory.resolve(strict=True)
    if not staging_directory.is_dir() or output_path.parent != staging_directory:
        raise MetadataWriteError('metadata output must be directly inside controlled staging')
    if output_path.suffix.casefold() not in {'.flac', '.ogg', '.opus', '.mp3', '.m4a', '.mp4'}:
        raise MetadataWriteError('canonical metadata output must be a FLAC, Ogg Vorbis, Opus, MP3, or MP4 file')
    if source_path == output_path or output_path.exists():
        raise MetadataWriteError('metadata source and destination must be distinct and unused')
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
        ('PERFORMER', metadata.performer),
    )
    return required + tuple((name, value) for name, value in optional if value is not None)


def _copy_to_staging(source_path: Path, staging_directory: Path, suffix: str) -> Path:
    inspection = inspect_media_capability(source_path)
    if inspection.capability is None and suffix.casefold() != '.mp3':
        raise MetadataWriteError('source has no declared capability')
    descriptor, name = tempfile.mkstemp(prefix='.metadata-', suffix=suffix, dir=staging_directory)
    temporary_path = Path(name)
    try:
        with os.fdopen(descriptor, 'wb') as temporary_file, source_path.open('rb') as source_file:
            shutil.copyfileobj(source_file, temporary_file)
            temporary_file.flush()
            os.fsync(temporary_file.fileno())
    except OSError as error:
        temporary_path.unlink(missing_ok=True)
        raise MetadataWriteError('unable to stage media for metadata writing') from error
    return temporary_path
