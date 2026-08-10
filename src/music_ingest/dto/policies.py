from collections.abc import Mapping
from typing import ClassVar, Literal

from pydantic import BaseModel, ConfigDict, field_validator, model_validator

ALLOWED_TAG_KEYS = frozenset(
    {
        'TITLE',
        'ARTIST',
        'ALBUM',
        'ALBUMARTIST',
        'DATE',
        'ORIGINALDATE',
        'TRACKNUMBER',
        'TRACKTOTAL',
        'DISCNUMBER',
        'DISCTOTAL',
        'GENRE',
        'MUSICBRAINZ_TRACKID',
        'MUSICBRAINZ_ALBUMID',
        'MUSICBRAINZ_RELEASEGROUPID',
        'ISRC',
    }
)


class FieldPolicy(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    schema_version: Literal[1]
    list_separator: Literal['; ']
    allowed_tag_keys: tuple[str, ...]

    @field_validator('allowed_tag_keys')
    @classmethod
    def allowed_tag_keys_are_exact(cls, keys: tuple[str, ...]) -> tuple[str, ...]:
        if frozenset(keys) != ALLOWED_TAG_KEYS or len(keys) != len(ALLOWED_TAG_KEYS):
            raise ValueError('allowed_tag_keys must contain exactly the canonical output keys')
        return keys


class GenrePolicy(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    schema_version: Literal[1]
    canonical_genres: tuple[str, ...]
    aliases: Mapping[str, tuple[str, ...]]

    @model_validator(mode='after')
    def aliases_target_canonical_genres(self) -> GenrePolicy:
        canonical_genres = frozenset(self.canonical_genres)
        if not canonical_genres:
            raise ValueError('canonical_genres must not be empty')
        if any(not alias.strip() for alias in self.aliases):
            raise ValueError('aliases must have non-empty source names')
        if any(
            not target or any(genre not in canonical_genres for genre in target) for target in self.aliases.values()
        ):
            raise ValueError('aliases must target only configured canonical_genres')
        return self
