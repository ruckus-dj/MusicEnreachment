from __future__ import annotations

import pytest
from pydantic import ValidationError

from music_ingest.dto.policies import ALLOWED_TAG_KEYS, FieldPolicy, GenrePolicy


def test_field_policy_when_allowed_tag_keys_matches_the_canonical_set_is_accepted() -> None:
    policy = FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=tuple(sorted(ALLOWED_TAG_KEYS)))
    assert frozenset(policy.allowed_tag_keys) == ALLOWED_TAG_KEYS


def test_field_policy_when_allowed_tag_keys_is_missing_a_canonical_key_is_rejected() -> None:
    incomplete = tuple(sorted(ALLOWED_TAG_KEYS))[:-1]
    with pytest.raises(ValidationError):
        _ = FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=incomplete)


def test_field_policy_when_allowed_tag_keys_has_an_unknown_extra_key_is_rejected() -> None:
    extra = (*sorted(ALLOWED_TAG_KEYS), 'UNKNOWN_KEY')
    with pytest.raises(ValidationError):
        _ = FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=extra)


def test_field_policy_when_allowed_tag_keys_has_a_duplicate_is_rejected() -> None:
    duplicated = (*sorted(ALLOWED_TAG_KEYS), next(iter(ALLOWED_TAG_KEYS)))
    with pytest.raises(ValidationError):
        _ = FieldPolicy(schema_version=1, list_separator='; ', allowed_tag_keys=duplicated)


def test_genre_policy_when_aliases_target_only_canonical_genres_is_accepted() -> None:
    policy = GenrePolicy(
        schema_version=1,
        canonical_genres=('Hip Hop', 'Rock'),
        aliases={'hip-hop': ('Hip Hop',), 'rock and roll': ('Rock',)},
    )
    assert policy.canonical_genres == ('Hip Hop', 'Rock')


def test_genre_policy_when_canonical_genres_is_empty_is_rejected() -> None:
    with pytest.raises(ValidationError):
        _ = GenrePolicy(schema_version=1, canonical_genres=(), aliases={})


def test_genre_policy_when_an_alias_source_name_is_blank_is_rejected() -> None:
    with pytest.raises(ValidationError):
        _ = GenrePolicy(schema_version=1, canonical_genres=('Rock',), aliases={'  ': ('Rock',)})


def test_genre_policy_when_an_alias_targets_a_genre_outside_canonical_genres_is_rejected() -> None:
    with pytest.raises(ValidationError):
        _ = GenrePolicy(schema_version=1, canonical_genres=('Rock',), aliases={'jazz-fusion': ('Jazz',)})


def test_genre_policy_when_an_alias_has_no_targets_is_rejected() -> None:
    with pytest.raises(ValidationError):
        _ = GenrePolicy(schema_version=1, canonical_genres=('Rock',), aliases={'rock and roll': ()})
