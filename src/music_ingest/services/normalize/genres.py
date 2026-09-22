from __future__ import annotations

from dataclasses import dataclass
from typing import override

from music_ingest.contracts import GenrePolicy
from music_ingest.services.normalize.genre_names import genre_key


@dataclass(frozen=True, slots=True)
class GenreNormalizationError(Exception):
    value: str

    @override
    def __str__(self) -> str:
        return f'genre is not approved by policy: {self.value}'


def normalize_genres(values: tuple[str, ...], policy: GenrePolicy) -> tuple[str, ...]:
    """Map approved genre aliases to policy-ordered canonical names."""
    aliases = {genre_key(name): target for name, target in policy.aliases.items()}
    canonical = {genre_key(name): (name,) for name in policy.canonical_genres}
    resolved: set[str] = set()
    for value in values:
        target = aliases.get(genre_key(value), canonical.get(genre_key(value)))
        if target is None:
            raise GenreNormalizationError(value)
        resolved.update(target)
    return tuple(genre for genre in policy.canonical_genres if genre in resolved)
