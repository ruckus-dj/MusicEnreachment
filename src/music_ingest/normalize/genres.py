from __future__ import annotations

import re
from dataclasses import dataclass
from typing import override

from music_ingest.config.policies import GenrePolicy

_GENRE_TEXT = re.compile(r'[^\w]+', re.UNICODE)


@dataclass(frozen=True, slots=True)
class GenreNormalizationError(Exception):
    value: str

    @override
    def __str__(self) -> str:
        return f'genre is not approved by policy: {self.value}'


def normalize_genres(values: tuple[str, ...], policy: GenrePolicy) -> tuple[str, ...]:
    """Map approved genre aliases to policy-ordered canonical names."""
    aliases = {_genre_key(name): target for name, target in policy.aliases.items()}
    canonical = {_genre_key(name): (name,) for name in policy.canonical_genres}
    resolved: set[str] = set()
    for value in values:
        target = aliases.get(_genre_key(value), canonical.get(_genre_key(value)))
        if target is None:
            raise GenreNormalizationError(value)
        resolved.update(target)
    return tuple(genre for genre in policy.canonical_genres if genre in resolved)


def _genre_key(value: str) -> str:
    return _GENRE_TEXT.sub(' ', value.casefold()).strip()
