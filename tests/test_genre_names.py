from __future__ import annotations

import pytest

from music_ingest.normalize.genre_names import display_genre_name, genre_key


@pytest.mark.parametrize(
    ('source_name', 'expected'),
    [
        ('hip hop', 'Hip Hop'),
        ('post-rock', 'Post Rock'),
        ('death metal', 'Death Metal'),
        ('  ambient  ', 'Ambient'),
        ('a', 'A'),
    ],
)
def test_display_genre_name_when_no_special_label_title_cases_each_word(source_name: str, expected: str) -> None:
    assert display_genre_name(source_name) == expected


@pytest.mark.parametrize(
    ('source_name', 'expected'),
    [
        ('dnb', 'DnB'),
        ('DNB', 'DnB'),
        ('DnB', 'DnB'),
        ('edm', 'EDM'),
        ('idm', 'IDM'),
        ('j-pop', 'J-Pop'),
        ('K-POP', 'K-Pop'),
        ('r&b', 'R&B'),
    ],
)
def test_display_genre_name_when_source_matches_a_special_label_uses_the_fixed_casing(
    source_name: str, expected: str
) -> None:
    assert display_genre_name(source_name) == expected


def test_display_genre_name_when_source_is_empty_after_stripping_returns_empty_string() -> None:
    assert display_genre_name('   ') == ''


@pytest.mark.parametrize(
    ('left', 'right'),
    [
        ('Hip Hop', 'hip-hop'),
        ('Hip Hop', 'HIP   HOP'),
        ('Post-Rock', 'Post   Rock'),
        ('R&B', 'r b'),
    ],
)
def test_genre_key_when_genres_differ_only_by_case_or_punctuation_normalizes_to_the_same_key(
    left: str, right: str
) -> None:
    assert genre_key(left) == genre_key(right)


def test_genre_key_when_genres_are_meaningfully_different_produces_different_keys() -> None:
    assert genre_key('Hip Hop') != genre_key('Trip Hop')
