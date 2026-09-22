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
from mutagen.id3 import ID3, TALB, TCON, TDOR, TDRC, TIT2, TPE1, TPE2, TPOS, TRCK, TSRC, TXXX, UFID
from mutagen.mp4 import MP4
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis

from music_ingest.adapters.inspectors._tool import ToolState, run_tool
from music_ingest.adapters.inspectors.media_capabilities import MediaCapability, inspect_media_capability
from music_ingest.contracts import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy
from music_ingest.services.normalize.genres import GenreNormalizationError, normalize_genres
from music_ingest.services.normalize.tags import MetadataTagError, write_normalized_tags


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
    musicbrainz_recording_id: str | None
    musicbrainz_artist_ids: tuple[str, ...]
    musicbrainz_album_artist_ids: tuple[str, ...]
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
    capability: MediaCapability | None = None
    ffmpeg_command: str = 'ffmpeg'
    ffprobe_command: str = 'ffprobe'
    timeout_seconds: float = 10.0


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
    if path.suffix.casefold() == '.mka':
        temporary_path = path.with_name(f'.{path.name}.metadata')
        _write_mka_tags(path, temporary_path, filtered, 'ffmpeg', 'ffprobe', 10.0)
        temporary_path.replace(path)
        return MetadataWriteResult(path, filtered)
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
    """Write only canonical metadata into a new staged publication."""
    source_path, output_path, staging_directory = _validate_paths(request)
    tags = _canonical_tags(request.metadata, request.fields, request.genres)
    temporary_path = _copy_to_staging(source_path, staging_directory, output_path.suffix, request.capability)
    try:
        if output_path.suffix.casefold() == '.mka':
            _write_mka_tags(
                temporary_path,
                output_path,
                tags,
                request.ffmpeg_command,
                request.ffprobe_command,
                request.timeout_seconds,
            )
        elif output_path.suffix.casefold() == '.mp3':
            _write_mp3_tags(temporary_path, request.metadata)
            _verify_mp3_tags(temporary_path, request.metadata)
        elif output_path.suffix.casefold() in {'.m4a', '.mp4'}:
            _write_mp4_tags(temporary_path, request.metadata)
            _verify_mp4_tags(temporary_path, request.metadata)
        else:
            _write_vorbis_comments(temporary_path, tags, output_path.suffix)
            _verify_vorbis_comments(temporary_path, tags, output_path.suffix)
        if output_path.suffix.casefold() != '.mka':
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
        comments: dict[str, list[str]] = {}
        for name, value in tags:
            comments.setdefault(name, []).append(value)
        for name, values in comments.items():
            audio[name] = values
        audio.save()
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen rejected canonical Vorbis comments') from error


def _verify_vorbis_comments(path: Path, expected: tuple[tuple[str, str], ...], suffix: str) -> None:
    try:
        audio = _open_vorbis_comments(path, suffix)
        actual = tuple((name.upper(), value) for name, values in audio.items() for value in values)
    except (MutagenError, OSError) as error:
        raise MetadataWriteError('Mutagen could not reopen canonical Vorbis comments') from error
    if frozenset(actual) != frozenset(expected):
        raise MetadataWriteError('Mutagen did not produce the required canonical tags')


def _write_mka_tags(
    source_path: Path,
    output_path: Path,
    tags: tuple[tuple[str, str], ...],
    ffmpeg_command: str,
    ffprobe_command: str,
    timeout_seconds: float,
) -> None:
    metadata_arguments = tuple(
        argument for name, value in _collapse_tags(tags).items() for argument in ('-metadata', f'{name}={value}')
    )
    evidence = run_tool(
        (
            ffmpeg_command,
            '-nostdin',
            '-hide_banner',
            '-v',
            'error',
            '-xerror',
            '-i',
            str(source_path),
            '-map',
            '0:a:0',
            '-map_metadata',
            '-1',
            '-map_chapters',
            '-1',
            '-c:a',
            'copy',
            *metadata_arguments,
            '-f',
            'matroska',
            '-n',
            str(output_path),
        ),
        timeout_seconds,
    )
    if evidence.state is not ToolState.SUCCESS or not output_path.is_file():
        output_path.unlink(missing_ok=True)
        raise MetadataWriteError('FFmpeg could not write canonical Matroska metadata')
    actual = _read_mka_tags(output_path, ffprobe_command, timeout_seconds)
    expected = _collapse_tags(tags)
    if actual != expected:
        output_path.unlink(missing_ok=True)
        raise MetadataWriteError('FFprobe did not observe the required canonical Matroska tags')


def _read_mka_tags(path: Path, ffprobe_command: str, timeout_seconds: float) -> dict[str, str]:
    evidence = run_tool(
        (
            ffprobe_command,
            '-v',
            'error',
            '-show_entries',
            'format_tags',
            '-of',
            'default=noprint_wrappers=1:nokey=0',
            str(path),
        ),
        timeout_seconds,
    )
    if evidence.state is not ToolState.SUCCESS:
        raise MetadataWriteError('FFprobe could not reopen Matroska metadata')
    values: dict[str, list[str]] = {}
    for line in evidence.stdout.splitlines():
        if not line.startswith('TAG:') or '=' not in line:
            continue
        name, value = line[4:].split('=', 1)
        values.setdefault(name.upper(), []).append(value)
    return {name: '; '.join(items) for name, items in values.items() if name in ALLOWED_TAG_KEYS}


def _collapse_tags(tags: tuple[tuple[str, str], ...]) -> dict[str, str]:
    values: dict[str, list[str]] = {}
    for name, value in tags:
        values.setdefault(name, []).append(value)
    return {name: '; '.join(items) for name, items in values.items()}


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
        if metadata.musicbrainz_recording_id is not None:
            tags.add(UFID(owner='http://musicbrainz.org', data=metadata.musicbrainz_recording_id.encode()))
        if metadata.musicbrainz_artist_ids:
            tags.add(TXXX(encoding=3, desc='MusicBrainz Artist Id', text=list(metadata.musicbrainz_artist_ids)))
        if metadata.musicbrainz_album_artist_ids:
            tags.add(
                TXXX(encoding=3, desc='MusicBrainz Album Artist Id', text=list(metadata.musicbrainz_album_artist_ids))
            )
        if metadata.musicbrainz_album_id is not None:
            tags.add(TXXX(encoding=3, desc='MusicBrainz Album Id', text=metadata.musicbrainz_album_id))
        if metadata.musicbrainz_release_group_id is not None:
            tags.add(
                TXXX(
                    encoding=3,
                    desc='MusicBrainz Release Group Id',
                    text=metadata.musicbrainz_release_group_id,
                )
            )
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
        ufid = tags.getall('UFID:http://musicbrainz.org')
        if metadata.musicbrainz_recording_id is not None and (
            not ufid or ufid[0].data.decode() != metadata.musicbrainz_recording_id
        ):
            raise MetadataWriteError('Mutagen did not produce the required MusicBrainz UFID')
        expected_txxx = {
            'MusicBrainz Artist Id': metadata.musicbrainz_artist_ids,
            'MusicBrainz Album Artist Id': metadata.musicbrainz_album_artist_ids,
            'MusicBrainz Album Id': () if metadata.musicbrainz_album_id is None else (metadata.musicbrainz_album_id,),
            'MusicBrainz Release Group Id': (
                () if metadata.musicbrainz_release_group_id is None else (metadata.musicbrainz_release_group_id,)
            ),
        }
        actual_txxx = {frame.desc: tuple(frame.text) for frame in tags.getall('TXXX')}
        if actual_txxx != {name: values for name, values in expected_txxx.items() if values}:
            raise MetadataWriteError('Mutagen did not produce the required MusicBrainz ID3 frames')
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
        if metadata.musicbrainz_recording_id is not None:
            audio['----:com.apple.iTunes:MusicBrainz Track Id'] = [metadata.musicbrainz_recording_id.encode()]
        if metadata.musicbrainz_artist_ids:
            audio['----:com.apple.iTunes:MusicBrainz Artist Id'] = [
                item.encode() for item in metadata.musicbrainz_artist_ids
            ]
        if metadata.musicbrainz_album_artist_ids:
            audio['----:com.apple.iTunes:MusicBrainz Album Artist Id'] = [
                item.encode() for item in metadata.musicbrainz_album_artist_ids
            ]
        if metadata.musicbrainz_album_id is not None:
            audio['----:com.apple.iTunes:MusicBrainz Album Id'] = [metadata.musicbrainz_album_id.encode()]
        if metadata.musicbrainz_release_group_id is not None:
            audio['----:com.apple.iTunes:MusicBrainz Release Group Id'] = [
                metadata.musicbrainz_release_group_id.encode()
            ]
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
                [metadata.musicbrainz_recording_id.encode()] if metadata.musicbrainz_recording_id is not None else None
            ),
            '----:com.apple.iTunes:MusicBrainz Artist Id': (
                [item.encode() for item in metadata.musicbrainz_artist_ids] if metadata.musicbrainz_artist_ids else None
            ),
            '----:com.apple.iTunes:MusicBrainz Album Artist Id': (
                [item.encode() for item in metadata.musicbrainz_album_artist_ids]
                if metadata.musicbrainz_album_artist_ids
                else None
            ),
            '----:com.apple.iTunes:MusicBrainz Album Id': (
                [metadata.musicbrainz_album_id.encode()] if metadata.musicbrainz_album_id is not None else None
            ),
            '----:com.apple.iTunes:MusicBrainz Release Group Id': (
                [metadata.musicbrainz_release_group_id.encode()]
                if metadata.musicbrainz_release_group_id is not None
                else None
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
    if output_path.suffix.casefold() not in {'.flac', '.ogg', '.opus', '.mp3', '.m4a', '.mp4', '.mka'}:
        raise MetadataWriteError('canonical metadata output must be a supported audio publication file')
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
                metadata.musicbrainz_recording_id,
                *metadata.musicbrainz_artist_ids,
                *metadata.musicbrainz_album_artist_ids,
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
        ('MUSICBRAINZ_RECORDINGID', metadata.musicbrainz_recording_id),
        *(('MUSICBRAINZ_ARTISTID', item) for item in metadata.musicbrainz_artist_ids),
        *(('MUSICBRAINZ_ALBUMARTISTID', item) for item in metadata.musicbrainz_album_artist_ids),
        ('MUSICBRAINZ_ALBUMID', metadata.musicbrainz_album_id),
        ('MUSICBRAINZ_RELEASEGROUPID', metadata.musicbrainz_release_group_id),
        ('ISRC', metadata.isrc),
        ('PERFORMER', metadata.performer),
    )
    return required + tuple((name, value) for name, value in optional if value is not None)


def _copy_to_staging(
    source_path: Path, staging_directory: Path, suffix: str, capability: MediaCapability | None = None
) -> Path:
    inspection = inspect_media_capability(source_path) if capability is None else None
    if capability is None and (inspection is None or inspection.capability is None) and suffix.casefold() != '.mp3':
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
