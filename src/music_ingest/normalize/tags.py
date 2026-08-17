from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from pathlib import Path

from mutagen import File, MutagenError
from mutagen.flac import FLAC
from mutagen.id3 import ID3, TALB, TCON, TDOR, TDRC, TIT2, TPE1, TPE2, TPE3, TPOS, TRCK, TSRC, TXXX, UFID
from mutagen.mp4 import MP4
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis

from music_ingest.dto import ALLOWED_TAG_KEYS
from music_ingest.inspectors._tool import ToolState, run_tool

_LIST_SEPARATOR = '; '
_MP4_FREEFORM_PREFIX = '----:com.apple.iTunes:'
_MP4_FIELDS = {
    'TITLE': '©nam',
    'ARTIST': '©ART',
    'ALBUM': '©alb',
    'ALBUMARTIST': 'aART',
    'DATE': '©day',
    'GENRE': '©gen',
}
_MP4_FREEFORM_FIELDS = {
    'ORIGINALDATE': 'ORIGINALDATE',
    'MUSICBRAINZ_RECORDINGID': 'MusicBrainz Track Id',
    'MUSICBRAINZ_ARTISTID': 'MusicBrainz Artist Id',
    'MUSICBRAINZ_ALBUMARTISTID': 'MusicBrainz Album Artist Id',
    'MUSICBRAINZ_ALBUMID': 'MusicBrainz Album Id',
    'MUSICBRAINZ_RELEASEGROUPID': 'MusicBrainz Release Group Id',
    'ISRC': 'ISRC',
    'PERFORMER': 'PERFORMER',
}
_ID3_TEXT_FIELDS = {
    'TITLE': TIT2,
    'ARTIST': TPE1,
    'ALBUM': TALB,
    'ALBUMARTIST': TPE2,
    'PERFORMER': TPE3,
    'DATE': TDRC,
    'ORIGINALDATE': TDOR,
    'GENRE': TCON,
    'ISRC': TSRC,
}
_ID3_TEXT_FRAME_NAMES = {
    'TITLE': 'TIT2',
    'ARTIST': 'TPE1',
    'ALBUM': 'TALB',
    'ALBUMARTIST': 'TPE2',
    'PERFORMER': 'TPE3',
    'DATE': 'TDRC',
    'ORIGINALDATE': 'TDOR',
    'GENRE': 'TCON',
    'ISRC': 'TSRC',
}
_ID3_TXXX_FIELDS = {
    'MUSICBRAINZ_ARTISTID': 'MusicBrainz Artist Id',
    'MUSICBRAINZ_ALBUMARTISTID': 'MusicBrainz Album Artist Id',
    'MUSICBRAINZ_ALBUMID': 'MusicBrainz Album Id',
    'MUSICBRAINZ_RELEASEGROUPID': 'MusicBrainz Release Group Id',
}


@dataclass(frozen=True, slots=True)
class MetadataTagError(Exception):
    path: Path

    def __str__(self) -> str:
        return f'Mutagen could not process metadata: {self.path}'


def read_normalized_tags(path: Path) -> tuple[tuple[str, str], ...]:
    """Read tags from any Mutagen-recognized audio container into canonical names."""
    if path.suffix.casefold() == '.mka':
        return _read_mka(path)
    try:
        audio = File(path, easy=True)
        if audio is None:
            raise MetadataTagError(path)
        if audio.tags is None:
            return ()
        values: dict[str, str] = {}
        aliases = {
            'title': 'TITLE',
            'artist': 'ARTIST',
            'album': 'ALBUM',
            'albumartist': 'ALBUMARTIST',
            'album artist': 'ALBUMARTIST',
            'date': 'DATE',
            'originaldate': 'ORIGINALDATE',
            'tracknumber': 'TRACKNUMBER',
            'tracktotal': 'TRACKTOTAL',
            'discnumber': 'DISCNUMBER',
            'disctotal': 'DISCTOTAL',
            'genre': 'GENRE',
            'musicbrainz_recordingid': 'MUSICBRAINZ_RECORDINGID',
            'musicbrainz_artistid': 'MUSICBRAINZ_ARTISTID',
            'musicbrainz_albumartistid': 'MUSICBRAINZ_ALBUMARTISTID',
            'musicbrainz_albumid': 'MUSICBRAINZ_ALBUMID',
            'musicbrainz_releasegroupid': 'MUSICBRAINZ_RELEASEGROUPID',
            'isrc': 'ISRC',
            'performer': 'PERFORMER',
        }
        tag_items = getattr(audio.tags, 'items', None)
        if not isinstance(tag_items, Callable):
            return ()
        for name, raw_values in tag_items():
            canonical_name = aliases.get(str(name).casefold())
            if canonical_name is None or not raw_values:
                continue
            values[canonical_name] = (
                _LIST_SEPARATOR.join(str(value) for value in raw_values)
                if isinstance(raw_values, list)
                else str(raw_values)
            )
        position = values.get('TRACKNUMBER')
        if position is not None and '/' in position:
            number, total = position.split('/', 1)
            values['TRACKNUMBER'] = number
            if total:
                values['TRACKTOTAL'] = total
        position = values.get('DISCNUMBER')
        if position is not None and '/' in position:
            number, total = position.split('/', 1)
            values['DISCNUMBER'] = number
            if total:
                values['DISCTOTAL'] = total
        return tuple((name, value) for name, value in values.items() if name in ALLOWED_TAG_KEYS)
    except (MutagenError, OSError, KeyError, UnicodeDecodeError) as error:
        raise MetadataTagError(path) from error


def _read_mka(path: Path) -> tuple[tuple[str, str], ...]:
    evidence = run_tool(
        (
            'ffprobe',
            '-v',
            'error',
            '-show_entries',
            'format_tags',
            '-of',
            'default=noprint_wrappers=1:nokey=0',
            str(path),
        ),
        10.0,
    )
    if evidence.state is not ToolState.SUCCESS:
        raise MetadataTagError(path)
    aliases = {
        'title': 'TITLE',
        'artist': 'ARTIST',
        'album': 'ALBUM',
        'albumartist': 'ALBUMARTIST',
        'album artist': 'ALBUMARTIST',
        'date': 'DATE',
        'originaldate': 'ORIGINALDATE',
        'tracknumber': 'TRACKNUMBER',
        'tracktotal': 'TRACKTOTAL',
        'discnumber': 'DISCNUMBER',
        'disctotal': 'DISCTOTAL',
        'genre': 'GENRE',
        'musicbrainz_recordingid': 'MUSICBRAINZ_RECORDINGID',
        'musicbrainz_artistid': 'MUSICBRAINZ_ARTISTID',
        'musicbrainz_albumartistid': 'MUSICBRAINZ_ALBUMARTISTID',
        'musicbrainz_albumid': 'MUSICBRAINZ_ALBUMID',
        'musicbrainz_releasegroupid': 'MUSICBRAINZ_RELEASEGROUPID',
        'isrc': 'ISRC',
        'performer': 'PERFORMER',
    }
    values: dict[str, list[str]] = {}
    for line in evidence.stdout.splitlines():
        if not line.startswith('TAG:') or '=' not in line:
            continue
        name, value = line[4:].split('=', 1)
        canonical_name = aliases.get(name.casefold())
        if canonical_name is not None:
            values.setdefault(canonical_name, []).append(value)
    normalized = {name: _LIST_SEPARATOR.join(items) for name, items in values.items()}
    position = normalized.get('TRACKNUMBER')
    if position is not None and '/' in position:
        number, total = position.split('/', 1)
        normalized['TRACKNUMBER'] = number
        if total:
            normalized['TRACKTOTAL'] = total
    position = normalized.get('DISCNUMBER')
    if position is not None and '/' in position:
        number, total = position.split('/', 1)
        normalized['DISCNUMBER'] = number
        if total:
            normalized['DISCTOTAL'] = total
    return tuple((name, value) for name, value in normalized.items() if name in ALLOWED_TAG_KEYS)


def write_normalized_tags(path: Path, tags: tuple[tuple[str, str], ...]) -> tuple[tuple[str, str], ...]:
    """Replace supported container tags with allowlisted normalized metadata."""
    filtered = tuple((name, value) for name, value in tags if name in ALLOWED_TAG_KEYS)
    values = dict(filtered)
    try:
        match path.suffix.casefold():
            case '.flac' | '.ogg' | '.opus':
                _write_vorbis(path, values)
            case '.mp3':
                _write_mp3(path, values)
            case '.m4a' | '.mp4':
                _write_mp4(path, values)
            case _:
                raise MetadataTagError(path)
    except (MutagenError, OSError, KeyError, UnicodeDecodeError) as error:
        raise MetadataTagError(path) from error
    actual = read_normalized_tags(path)
    if dict(actual) != values:
        raise MetadataTagError(path)
    return filtered


def _read_vorbis(path: Path) -> tuple[tuple[str, str], ...]:
    audio = _open_vorbis(path)
    return tuple(
        (name.upper(), _LIST_SEPARATOR.join(values))
        for name, values in audio.items()
        if name.upper() in ALLOWED_TAG_KEYS and values
    )


def _write_vorbis(path: Path, values: Mapping[str, str]) -> None:
    audio = _open_vorbis(path)
    audio.clear()
    for name, value in values.items():
        audio[name] = [value]
    audio.save()


def _open_vorbis(path: Path) -> FLAC | OggVorbis | OggOpus:
    match path.suffix.casefold():
        case '.flac':
            return FLAC(path)
        case '.ogg':
            return OggVorbis(path)
        case '.opus':
            return OggOpus(path)
        case _:
            raise MetadataTagError(path)


def _read_mp3(path: Path) -> tuple[tuple[str, str], ...]:
    tags = ID3(path)
    values: dict[str, str] = {}
    for name, frame_name in _ID3_TEXT_FRAME_NAMES.items():
        frame = tags.get(frame_name)
        if frame is not None:
            values[name] = _LIST_SEPARATOR.join(str(value) for value in frame.text)
    _read_position(tags, 'TRCK', 'TRACKNUMBER', 'TRACKTOTAL', values)
    _read_position(tags, 'TPOS', 'DISCNUMBER', 'DISCTOTAL', values)
    for name, description in _ID3_TXXX_FIELDS.items():
        frame = next((item for item in tags.getall('TXXX') if item.desc == description), None)
        if frame is not None:
            values[name] = _LIST_SEPARATOR.join(frame.text)
    ufid = next((item for item in tags.getall('UFID') if item.owner == 'http://musicbrainz.org'), None)
    if ufid is not None:
        values['MUSICBRAINZ_RECORDINGID'] = ufid.data.decode()
    return tuple(values.items())


def _read_position(tags: ID3, frame_name: str, number_name: str, total_name: str, values: dict[str, str]) -> None:
    frame = tags.get(frame_name)
    if frame is None or not frame.text:
        return
    number, separator, total = str(frame.text[0]).partition('/')
    if number:
        values[number_name] = number
    if separator and total:
        values[total_name] = total


def _write_mp3(path: Path, values: Mapping[str, str]) -> None:
    tags = ID3()
    for name, frame_type in _ID3_TEXT_FIELDS.items():
        value = values.get(name)
        if value is not None:
            tags.add(frame_type(encoding=3, text=value.split(_LIST_SEPARATOR)))
    _add_position(tags, TRCK, values.get('TRACKNUMBER'), values.get('TRACKTOTAL'))
    _add_position(tags, TPOS, values.get('DISCNUMBER'), values.get('DISCTOTAL'))
    for name, description in _ID3_TXXX_FIELDS.items():
        value = values.get(name)
        if value is not None:
            tags.add(TXXX(encoding=3, desc=description, text=value))
    recording_id = values.get('MUSICBRAINZ_RECORDINGID')
    if recording_id is not None:
        tags.add(UFID(owner='http://musicbrainz.org', data=recording_id.encode()))
    tags.save(path, v2_version=4)


def _add_position(tags: ID3, frame_type: type[TRCK] | type[TPOS], number: str | None, total: str | None) -> None:
    if number is not None:
        tags.add(frame_type(encoding=3, text=f'{number}/{total}' if total is not None else number))


def _read_mp4(path: Path) -> tuple[tuple[str, str], ...]:
    tags = MP4(path).tags or {}
    values: dict[str, str] = {}
    for name, atom in _MP4_FIELDS.items():
        atom_values = tags.get(atom)
        if atom_values:
            values[name] = _LIST_SEPARATOR.join(str(value) for value in atom_values)
    _read_mp4_position(tags.get('trkn'), 'TRACKNUMBER', 'TRACKTOTAL', values)
    _read_mp4_position(tags.get('disk'), 'DISCNUMBER', 'DISCTOTAL', values)
    for name, atom_name in _MP4_FREEFORM_FIELDS.items():
        atom_values = tags.get(_MP4_FREEFORM_PREFIX + atom_name)
        if atom_values:
            values[name] = _LIST_SEPARATOR.join(value.decode() for value in atom_values)
    return tuple(values.items())


def _read_mp4_position(
    position: list[tuple[int, int]] | None,
    number_name: str,
    total_name: str,
    values: dict[str, str],
) -> None:
    if position:
        number, total = position[0]
        if number:
            values[number_name] = str(number)
        if total:
            values[total_name] = str(total)


def _write_mp4(path: Path, values: Mapping[str, str]) -> None:
    audio = MP4(path)
    audio.clear()
    for name, atom in _MP4_FIELDS.items():
        value = values.get(name)
        if value is not None:
            audio[atom] = value.split(_LIST_SEPARATOR)
    _write_mp4_position(audio, 'trkn', values.get('TRACKNUMBER'), values.get('TRACKTOTAL'))
    _write_mp4_position(audio, 'disk', values.get('DISCNUMBER'), values.get('DISCTOTAL'))
    for name, atom_name in _MP4_FREEFORM_FIELDS.items():
        value = values.get(name)
        if value is not None:
            audio[_MP4_FREEFORM_PREFIX + atom_name] = [value.encode()]
    audio.save()


def _write_mp4_position(audio: MP4, atom: str, number: str | None, total: str | None) -> None:
    if number is not None:
        audio[atom] = [(int(number), int(total) if total is not None else 0)]
