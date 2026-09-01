from __future__ import annotations

import re

_SPECIAL_LABELS = {
    'dnb': 'DnB',
    'edm': 'EDM',
    'idm': 'IDM',
    'j-pop': 'J-Pop',
    'k-pop': 'K-Pop',
    'r&b': 'R&B',
}
_GENRE_TEXT = re.compile(r'[^\w]+', re.UNICODE)


def display_genre_name(source_name: str) -> str:
    special = _SPECIAL_LABELS.get(source_name.casefold())
    if special is not None:
        return special
    return ' '.join(word[:1].upper() + word[1:] for word in re.split(r'[\s-]+', source_name.strip()) if word)


def genre_key(value: str) -> str:
    return _GENRE_TEXT.sub(' ', value.casefold()).strip()
