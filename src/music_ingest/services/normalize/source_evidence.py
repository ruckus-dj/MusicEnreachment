"""Read-only, bounded container evidence extraction; never synthesize original bytes.

Mutagen remains the canonical fallback for unsupported blocks. Physical text bytes
are prepared here (not in the recovery algorithm). Unknown/compressed ID3 frames
remain Unicode-only fallback rather than pretending reconstructed bytes are evidence.
"""

from dataclasses import dataclass
from pathlib import Path
from uuid import UUID

from mutagen import File

from music_ingest.contracts import ALLOWED_TAG_KEYS
from music_ingest.models import SourceTagRecord
from music_ingest.services.normalize.tags import MetadataTagError, read_normalized_tags

_LIMIT = 16 * 1024 * 1024
_FIELD_LIMIT = 4096


class EvidenceLimitError(MetadataTagError):
    """Explicit extraction failure; no partial observations may be persisted."""

    reason: str

    def __init__(self, reason: str) -> None:
        super().__init__(Path('.'))
        object.__setattr__(self, 'reason', reason)

    def __str__(self) -> str:
        return self.reason


@dataclass
class _EvidenceBudget:
    size: int = 0
    count: int = 0

    def reserve(self, size: int) -> None:
        if self.count >= _FIELD_LIMIT:
            raise EvidenceLimitError('metadata evidence exceeds field count limit')
        if self.size + size > _LIMIT:
            raise EvidenceLimitError('metadata aggregate evidence exceeds size limit')
        self.size += size
        self.count += 1


_ID3 = {
    'TIT2': 'TITLE',
    'TPE1': 'ARTIST',
    'TALB': 'ALBUM',
    'TPE2': 'ALBUMARTIST',
    'TPE3': 'PERFORMER',
    'TDRC': 'DATE',
    'TYER': 'DATE',
    'TDOR': 'ORIGINALDATE',
    'TORY': 'ORIGINALDATE',
    'TCON': 'GENRE',
    'TRCK': 'TRACKNUMBER',
    'TPOS': 'DISCNUMBER',
    'TSRC': 'ISRC',
}
_ASF = {
    'Title': 'TITLE',
    'Author': 'ARTIST',
    'WM/AlbumTitle': 'ALBUM',
    'WM/AlbumArtist': 'ALBUMARTIST',
    'WM/Year': 'DATE',
    'WM/Genre': 'GENRE',
    'WM/TrackNumber': 'TRACKNUMBER',
    'WM/PartOfSet': 'DISCNUMBER',
}
_TXXX = {
    'MusicBrainz Album Id': 'MUSICBRAINZ_ALBUMID',
    'MusicBrainz Artist Id': 'MUSICBRAINZ_ARTISTID',
    'MusicBrainz Album Artist Id': 'MUSICBRAINZ_ALBUMARTISTID',
    'MusicBrainz Release Group Id': 'MUSICBRAINZ_RELEASEGROUPID',
    'MusicBrainz Track Id': 'MUSICBRAINZ_RECORDINGID',
}


def _sync(data: bytes) -> int:
    if any(value & 0x80 for value in data):
        raise ValueError('invalid synchsafe size')
    result = 0
    for value in data:
        result = (result << 7) | value
    return result


def _parts(data: bytes, codec: str) -> list[bytes]:
    width = 2 if codec.startswith('utf-16') else 1
    parts: list[bytes] = []
    start = 0
    for offset in range(0, len(data) - width + 1, width):
        if data[offset : offset + width] == b'\0' * width:
            if len(parts) >= _FIELD_LIMIT:
                raise EvidenceLimitError('metadata evidence exceeds field count limit')
            parts.append(data[start:offset])
            start = offset + width
    if start < len(data) or not parts:
        parts.append(data[start:])
    return parts


def _field(
    budget: _EvidenceBudget, name: str, data: bytes, raw: bytes, codec: str, container: str, identity: str
) -> SourceTagRecord:
    # Account for prepared bytes and both Unicode columns before constructing records.
    budget.reserve(len(raw) + 9 * len(data))
    try:
        value = data.decode(codec, errors='strict')
    except UnicodeError:
        value = ''  # Raw evidence retained; explicit decoding can recover it, never replacement decoding.
    return SourceTagRecord(
        tag_name=name,
        value=value,
        original_value=value,
        prepared_bytes=data,
        binary_evidence=raw,
        declared_codec=codec,
        format_name=container,
        physical_id=identity,
        extraction_version='source-evidence-v1',
        selected=True,
    )


def _id3_fields(body: bytes, version: int, flags: int, budget: _EvidenceBudget | None = None) -> list[SourceTagRecord]:
    budget = budget if budget is not None else _EvidenceBudget()
    if version not in (3, 4):
        return []
    # v2.3 tag-wide unsync changes frame boundaries; retain original block as binary evidence.
    original = body
    if version == 3 and flags & 0x80:
        body = body.replace(b'\xff\x00', b'\xff')
    offset = 0
    if flags & 0x40:
        offset = (4 + int.from_bytes(body[:4], 'big')) if version == 3 else _sync(body[:4])
    result: list[SourceTagRecord] = []
    while offset + 10 <= len(body):
        header = body[offset : offset + 10]
        if not header[:4].strip(b'\0'):
            break
        frame = header[:4].decode('ascii', errors='strict')
        size = _sync(header[4:8]) if version == 4 else int.from_bytes(header[4:8], 'big')
        end = offset + 10 + size
        if end > len(body) or size <= 0:
            break
        payload = body[offset + 10 : end]
        raw = original if version == 3 and flags & 0x80 else body[offset:end]
        identity = f'id3v2.{version}:{offset}:{frame}'
        offset = end
        # Do not decode compressed/encrypted/grouped frames without their transport semantics.
        if header[9] & (0xE0 if version == 3 else 0x4C):
            continue
        if version == 4:
            if flags & 0x80 or header[9] & 0x02:
                payload = payload.replace(b'\xff\x00', b'\xff')
            if header[9] & 0x01:
                payload = payload[4:]
        if not payload or payload[0] not in (0, 1, 2, 3):
            continue
        codec = ('latin-1', 'utf-16', 'utf-16-be', 'utf-8')[payload[0]]
        name = _ID3.get(frame)
        if name is None and frame != 'TXXX':
            continue
        chunks = _parts(payload[1:], codec)
        if frame == 'TXXX' and chunks:
            try:
                name = _TXXX.get(chunks.pop(0).decode(codec))
            except UnicodeError:
                continue
        if name is None:
            continue
        # A UTF16 BOM is per frame; subsequent values inherit its byte order.
        inherited = codec
        if codec == 'utf-16':
            if payload[1:3] == b'\xff\xfe':
                inherited = 'utf-16-le'
            elif payload[1:3] == b'\xfe\xff':
                inherited = 'utf-16-be'
        for index, data in enumerate(chunks):
            if codec == 'utf-16':
                if data.startswith(b'\xff\xfe'):
                    inherited = 'utf-16-le'
                elif data.startswith(b'\xfe\xff'):
                    inherited = 'utf-16-be'
                actual = codec if data.startswith((b'\xff\xfe', b'\xfe\xff')) else inherited
            else:
                actual = codec
            result.append(_field(budget, name, data, raw, actual, 'mp3', f'{identity}:{index}'))
    # Mutagen prefers modern date frames over their deprecated v2.3 aliases.
    # Shadow frames remain inspectable and recoverable, never concatenated into DATE.
    present = {field.physical_id.split(':')[2] for field in result if field.physical_id}
    for field in result:
        frame_name = field.physical_id.split(':')[2] if field.physical_id else ''
        if (frame_name == 'TYER' and 'TDRC' in present) or (frame_name == 'TORY' and 'TDOR' in present):
            field.selected = False
    return result


def _flac_fields(path: Path, budget: _EvidenceBudget) -> list[SourceTagRecord]:
    result: list[SourceTagRecord] = []
    with path.open('rb') as stream:
        stream.seek(4)
        total = 0
        while True:
            header = stream.read(4)
            if len(header) != 4:
                break
            size = int.from_bytes(header[1:], 'big')
            total += size
            if total > _LIMIT:
                raise ValueError('metadata evidence exceeds size limit')
            block_offset = stream.tell() - 4
            body = stream.read(size)
            if len(body) != size:
                raise ValueError('truncated FLAC metadata')
            if header[0] & 0x7F == 4:
                offset = 4 + int.from_bytes(body[:4], 'little')
                count = int.from_bytes(body[offset : offset + 4], 'little')
                offset += 4
                for index in range(min(count, len(body) // 4)):
                    length = int.from_bytes(body[offset : offset + 4], 'little')
                    raw = body[offset + 4 : offset + 4 + length]
                    offset += 4 + length
                    if b'=' not in raw:
                        continue
                    name_bytes, data = raw.split(b'=', 1)
                    name = name_bytes.decode('ascii').upper()
                    if name in ALLOWED_TAG_KEYS:
                        result.append(
                            _field(budget, name, data, raw, 'utf-8', 'flac', f'vorbis:{block_offset}:{index}')
                        )
            if header[0] & 0x80:
                break
    return result


def _asf_fields(body: bytes, budget: _EvidenceBudget) -> list[SourceTagRecord]:
    result: list[SourceTagRecord] = []
    offset = 30
    content = UUID('75b22633-668e-11cf-a6d9-00aa0062ce6c').bytes_le
    extended = UUID('d2d0a440-e307-11d2-97f0-00a0c95ea850').bytes_le
    while offset + 24 <= len(body):
        guid = body[offset : offset + 16]
        length = int.from_bytes(body[offset + 16 : offset + 24], 'little')
        if length < 24 or offset + length > len(body):
            break
        data = body[offset + 24 : offset + length]
        if guid == content and len(data) >= 10:
            cursor = 10
            for index, name in enumerate(('TITLE', 'ARTIST', '', '', '')):
                size = int.from_bytes(data[index * 2 : index * 2 + 2], 'little')
                raw = data[cursor : cursor + size]
                cursor += size
                if name:
                    prepared = raw[:-2] if raw.endswith(b'\0\0') else raw
                    result.append(_field(budget, name, prepared, raw, 'utf-16-le', 'asf', f'content:{offset}:{index}'))
        elif guid == extended:
            cursor = 2
            count = int.from_bytes(data[:2], 'little')
            for index in range(min(count, len(data) // 6)):
                size = int.from_bytes(data[cursor : cursor + 2], 'little')
                cursor += 2
                key = data[cursor : cursor + size].decode('utf-16-le').rstrip('\0')
                cursor += size
                kind = int.from_bytes(data[cursor : cursor + 2], 'little')
                size = int.from_bytes(data[cursor + 2 : cursor + 4], 'little')
                cursor += 4
                raw = data[cursor : cursor + size]
                cursor += size
                name = _ASF.get(key)
                if name and kind == 0:
                    prepared = raw[:-2] if raw.endswith(b'\0\0') else raw
                    result.append(
                        _field(budget, name, prepared, raw, 'utf-16-le', 'asf', f'extended:{offset}:{index}:{key}')
                    )
        offset += length
    return result


def read_source_fields(path: Path, normalized_tags: tuple[tuple[str, str], ...] | None = None) -> list[SourceTagRecord]:
    budget = _EvidenceBudget()
    audio = File(path, easy=False)
    container = type(audio).__name__.lower() if audio is not None else path.suffix.lstrip('.').lower()
    fields: list[SourceTagRecord] = []
    with path.open('rb') as stream:
        header = stream.read(30)
        if header.startswith(b'ID3'):
            size = _sync(header[6:10])
            if size > _LIMIT:
                raise ValueError('metadata evidence exceeds size limit')
            stream.seek(10)
            fields.extend(_id3_fields(stream.read(size), header[3], header[5], budget))
        elif header.startswith(b'fLaC'):
            fields.extend(_flac_fields(path, budget))
        elif header[:16] == UUID('75b22630-668e-11cf-a6d9-00aa0062ce6c').bytes_le:
            size = int.from_bytes(header[16:24], 'little')
            if size > _LIMIT:
                raise ValueError('metadata evidence exceeds size limit')
            stream.seek(0)
            fields.extend(_asf_fields(stream.read(size), budget))
        if container == 'mp3':
            stream.seek(0, 2)
            if stream.tell() >= 128:
                stream.seek(-128, 2)
                raw = stream.read(128)
                if raw.startswith(b'TAG'):
                    for name, start, end in [('TITLE', 3, 33), ('ARTIST', 33, 63), ('ALBUM', 63, 93), ('DATE', 93, 97)]:
                        data = raw[start:end].rstrip(b'\0 ')
                        item = _field(budget, name, data, raw[start:end], 'latin-1', 'mp3', f'id3v1:{start}:0')
                        item.selected = not any(field.tag_name == name for field in fields)
                        fields.append(item)
    # Public Mutagen reader supplies canonical tags for unsupported physical blocks, with bytes unavailable.
    covered = {field.tag_name for field in fields if field.selected}
    for name, value in read_normalized_tags(path) if normalized_tags is None else normalized_tags:
        if name not in covered:
            budget.reserve(8 * len(value))
            fields.append(
                SourceTagRecord(
                    format_name=container,
                    tag_name=name,
                    value=value,
                    original_value=value,
                    physical_id=f'mutagen:{name}',
                    selected=True,
                )
            )
    return fields
