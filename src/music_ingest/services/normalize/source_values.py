"""Canonical current source values, projected only from persisted observations."""

from collections.abc import Iterable

from mutagen.id3 import TCON

from music_ingest.contracts import ALLOWED_TAG_KEYS
from music_ingest.models.library import SourceTagView


def source_values(fields: Iterable[SourceTagView]) -> dict[str, str]:
    values: dict[str, list[str]] = {}
    for field in fields:
        if field.selected is not False and field.tag_name in ALLOWED_TAG_KEYS:
            if field.tag_name == 'GENRE' and field.format_name == 'mp3':
                values.setdefault(field.tag_name, []).extend(TCON(encoding=3, text=[field.value]).genres)
            else:
                values.setdefault(field.tag_name, []).append(field.value)
    result = {name: '; '.join(items) for name, items in values.items()}
    for number, total in [('TRACKNUMBER', 'TRACKTOTAL'), ('DISCNUMBER', 'DISCTOTAL')]:
        value = result.get(number)
        if value is not None and '/' in value:
            result[number], inferred = value.split('/', 1)
            if inferred and total not in result:
                result[total] = inferred
    return result
