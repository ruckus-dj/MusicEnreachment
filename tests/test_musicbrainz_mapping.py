from __future__ import annotations

import pytest

from music_ingest.contracts import Release
from music_ingest.services.matching.musicbrainz_mapping import candidate_for_release


def _release(**overrides: object) -> Release:
    base: dict[str, object] = {
        'id': 'release-id',
        'title': 'Fixture Album',
        'artist-credit': [{'name': 'Album Artist'}],
        'media': [
            {
                'position': 1,
                'tracks': [
                    {
                        'position': 1,
                        'title': 'Fixture Track',
                        'length': 210000,
                        'recording': {'id': 'recording-id', 'title': 'Fixture Track'},
                    }
                ],
            }
        ],
    }
    base.update(overrides)
    return Release.model_validate(base)


def test_candidate_for_release_when_recording_matches_a_regular_track_builds_a_candidate() -> None:
    # Given: a release whose one audio track matches the requested recording MBID.
    release = _release()

    # When: building a candidate for that recording.
    candidate = candidate_for_release(release, 'recording-id')

    # Then: the candidate carries the matched track's identity and duration.
    assert candidate is not None
    assert candidate.recording_mbids == ('recording-id',)
    assert candidate.recording_title == 'Fixture Track'
    assert candidate.duration_seconds == 210
    assert candidate.track_number == 1


def test_candidate_for_release_when_recording_only_appears_as_a_data_track_returns_none() -> None:
    # Given: the requested recording is a CD data track (e.g. a CD-ROM session), not audio.
    release = _release(
        media=[
            {
                'position': 1,
                'tracks': [],
                'data-tracks': [
                    {
                        'position': 1,
                        'title': 'Data Track',
                        'recording': {'id': 'recording-id', 'title': 'Data Track'},
                    }
                ],
            }
        ]
    )

    # When / Then: the recording is not a viable audio match, so no candidate is built.
    assert candidate_for_release(release, 'recording-id') is None


def test_candidate_for_release_when_recording_is_absent_still_builds_a_release_only_candidate() -> None:
    # Given: the release does not contain the requested recording at all (e.g. release lookup by ID).
    release = _release()

    # When: building a candidate for an unrelated recording MBID.
    candidate = candidate_for_release(release, 'other-recording-id')

    # Then: a release-level candidate is still returned, without track-specific fields.
    assert candidate is not None
    assert candidate.recording_title is None
    assert candidate.track_number is None
    assert candidate.duration_seconds is None


@pytest.mark.parametrize(
    ('raw_score', 'expected'),
    [
        (0.8, 80.0),
        (1.0, 100.0),
        (150.0, 150.0),
    ],
)
def test_candidate_for_release_normalizes_fractional_musicbrainz_scores_to_a_percentage(
    raw_score: float, expected: float
) -> None:
    # Given: MusicBrainz search scores may arrive as a 0..1 fraction or an already-scaled percentage.
    release = _release()

    # When: building a candidate with the raw provider score.
    candidate = candidate_for_release(release, 'recording-id', musicbrainz_score=raw_score)

    # Then: only the 0..1 range is scaled up; anything already percentage-like passes through.
    assert candidate is not None
    assert candidate.musicbrainz_score == expected


def test_candidate_for_release_when_artist_name_is_not_given_falls_back_to_release_artist_credit() -> None:
    # Given: no explicit artist name override is supplied.
    release = _release(**{'artist-credit': [{'name': 'Album Artist', 'joinphrase': ''}]})

    # When: building the candidate without an artist_name override.
    candidate = candidate_for_release(release, 'recording-id')

    # Then: the release artist credit is used as the candidate artist.
    assert candidate is not None
    assert candidate.artist_name == 'Album Artist'
    assert candidate.release_artist_name == 'Album Artist'
