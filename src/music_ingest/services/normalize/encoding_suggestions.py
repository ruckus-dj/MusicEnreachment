"""Read-only, language-specific proposals from persisted Unicode, never raw-byte guesses.

CP1251 is a limited legacy-Cyrillic prior, not universal language detection.
No proposal is selected or written automatically. Exact inverse roundtrip is required.
"""

import re

from music_ingest.contracts.source_encoding import Codec, EncodingChoice, EncodingField, EncodingSuggestion


def _candidate(text: str) -> tuple[str, Codec] | None:
    for codec in ('latin-1', 'cp1252'):
        try:
            value = text.encode(codec).decode('cp1251')
            if value != text and value.encode('cp1251').decode(codec) == text:
                return value, codec
        except UnicodeError:
            continue
    return None


def _dense(text: str) -> bool:
    letters = [c for c in text if c.isalpha() or ord(c) >= 128]
    count = sum(192 <= ord(c) <= 255 for c in letters)
    return count >= 4 and count / max(len(letters), 1) >= 0.6


def _shape(text: str) -> bool:
    letters = re.findall('[А-Яа-яЁё]', text)
    return len(letters) >= 4 and any(c.lower() in 'аеёиоуыэюя' for c in letters)


def _anchor(field: EncodingField) -> bool:
    candidate = _candidate(field.current_value)
    return bool(
        field.applied_choice is None
        and candidate
        and _dense(field.current_value)
        and _shape(candidate[0])
        and len(re.findall('[А-Яа-яЁё]', candidate[0])) >= 6
    )


def suggest_encodings(fields: list[EncodingField]) -> list[EncodingSuggestion]:
    result: list[EncodingSuggestion] = []
    for field in fields:
        text = field.current_value
        if field.applied_choice is not None or not text.strip() or '\ufffd' in text:
            continue
        candidate = _candidate(text)
        if candidate is None:
            continue
        value, codec = candidate
        context = any(
            sibling.selected
            and sibling.container == field.container
            and sibling.tag_name != field.tag_name
            and _anchor(sibling)
            for sibling in fields
        )
        plausible = _shape(value) and (_dense(text) or (context and re.search('[À-ÿ]{4,}', text) is not None))
        if plausible and (_anchor(field) or context):
            result.append(
                EncodingSuggestion(
                    field_id=field.field_id,
                    state='suggested',
                    value=value,
                    choice=EncodingChoice(
                        field_id=field.field_id, mode='unicode', encode_codec=codec, decode_codec='cp1251'
                    ),
                    reason=(
                        'Обратимый Unicode → CP1251; ограниченная эвристика кириллического корпуса, не уверенность. '
                        + ('Поддержка другого поля того же блока.' if context else 'Длинный плотный Latin-1 паттерн.')
                    ),
                )
            )
        elif _dense(text) or (len(text) <= 2 and any(ord(c) >= 128 for c in text)):
            result.append(
                EncodingSuggestion(
                    field_id=field.field_id,
                    state='review',
                    value=None,
                    choice=None,
                    reason='Короткое или неоднозначное поле: недостаточно оснований выбирать кодировку.',
                )
            )
    return result
