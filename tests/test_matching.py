from __future__ import annotations

import pytest

from music_ingest.matching.musicbrainz_mapping import merge_recording_candidate
from music_ingest.matching.providers import ReleaseCandidate
from music_ingest.matching.scoring import (
    CandidateScore,
    MatchingRequest,
    score_recording_candidate,
    score_release_candidate,
    select_folder_release,
)

RELEASE_MBID = '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'


def test_folder_release_selection_when_candidates_intersect_prefers_highest_score_sum() -> None:
    # Given: two source tracks share two releases, with one release scoring higher across the folder.
    candidate_scores = (
        (CandidateScore('release-one', 0.9), CandidateScore('release-two', 0.8)),
        (CandidateScore('release-one', 0.7), CandidateScore('release-two', 0.95)),
    )

    # When: the folder-level release is selected from the shared candidate intersection.
    selected = select_folder_release(candidate_scores)

    # Then: the release with the greatest combined score wins.
    assert selected == 'release-two'


def test_folder_release_selection_when_scores_tie_uses_stable_mbid_order() -> None:
    # Given: every source has the same two release candidates and equal total scores.
    candidate_scores = (
        (CandidateScore('release-two', 0.8), CandidateScore('release-one', 0.9)),
        (CandidateScore('release-one', 0.8), CandidateScore('release-two', 0.9)),
    )

    # When: the folder-level release is selected.
    selected = select_folder_release(candidate_scores)

    # Then: a deterministic member of the tied intersection is returned.
    assert selected == 'release-one'


def test_folder_release_selection_when_low_score_candidate_has_larger_sum_ignores_it() -> None:
    # Given: a low-confidence release would win the raw sum, but another release clears the threshold for every file.
    candidate_scores = (
        (CandidateScore('low-release', 0.69), CandidateScore('qualified-release', 0.71)),
        (CandidateScore('low-release', 0.99), CandidateScore('qualified-release', 0.72)),
    )

    # When: the folder-level release is selected using the configured confidence threshold.
    selected = select_folder_release(candidate_scores, confidence_threshold=0.70)

    # Then: only the release qualified for every source participates in the aggregate.
    assert selected == 'qualified-release'


def test_folder_release_selection_when_one_source_has_no_qualified_release_returns_none() -> None:
    # Given: one source has no release candidate at the configured confidence threshold.
    candidate_scores = (
        (CandidateScore('release-one', 0.9),),
        (CandidateScore('release-one', 0.69),),
    )

    # When: the folder-level release is selected.
    selected = select_folder_release(candidate_scores, confidence_threshold=0.70)

    # Then: no release is selected for only part of the folder.
    assert selected is None


def test_merge_recording_candidate_when_recording_titles_differ_keeps_one_consistent_recording() -> None:
    # Given: two projections of one release for different recordings.
    existing = ReleaseCandidate(
        'release-id',
        'Новый альбом',
        'Noize MC',
        216,
        ('bass-recording-id',),
        recording_title='Эдем 14/88',
        track_number=23,
    )
    candidate = ReleaseCandidate(
        'release-id',
        'Новый альбом',
        'Noize MC',
        421,
        ('love-recording-id',),
        recording_title='Бассейн',
        track_number=9,
    )

    # When: projections are merged for the same release.
    merged = merge_recording_candidate(existing, candidate)

    # Then: the selected projection does not advertise another recording's MBID.
    assert merged.recording_mbids == ('bass-recording-id', 'love-recording-id')
    assert [(item.recording_mbids, item.recording_title) for item in merged.recording_candidates] == [
        (('bass-recording-id',), 'Эдем 14/88'),
        (('love-recording-id',), 'Бассейн'),
    ]


def test_matching_when_source_album_is_missing_normalizes_available_release_evidence() -> None:
    # Given: a fresh release candidate with exact artist and duration evidence but no source album.
    request = MatchingRequest('Noize MC', '', 138, recording_title='Страна дождей')
    candidate = ReleaseCandidate(
        'release-id',
        'Страна дождей',
        'Noize MC',
        138,
        recording_mbids=('recording-id',),
        recording_title='Страна дождей',
    )
    # When: matching scores the release using only the source evidence that exists.
    score = score_release_candidate(request, candidate)

    # Then: the missing album does not reduce exact available evidence below the threshold.
    assert score.score == 1.0


def test_matching_when_release_duration_is_missing_normalizes_available_evidence() -> None:
    # Given: a release candidate with exact artist and album evidence but no duration.
    request = MatchingRequest('Noize MC', 'Страна дождей', 138)
    candidate = ReleaseCandidate('release-id', 'Страна дождей', 'Noize MC', None)

    # When: matching scores the release without duration evidence.
    score = score_release_candidate(request, candidate)

    # Then: the available artist and album evidence uses the full local score range.
    assert score.score == 1.0
    assert score.artist_component == 0.5
    assert score.release_component == 0.5
    assert score.duration_component == 0.0


def test_matching_when_musicbrainz_search_score_is_present_weights_provider_evidence() -> None:
    # Given: a candidate whose local evidence is incomplete but MusicBrainz ranked it exactly.
    request = MatchingRequest('кис-кис', 'Харакири', 164)
    candidate = ReleaseCandidate(
        'release-id',
        'харакири (трибьют Егору Летову)',
        'кис-кис',
        164,
        musicbrainz_score=100,
    )

    # When: the release candidate is scored with its provider search score.
    score = score_release_candidate(request, candidate)

    # Then: the weighted score stays bounded and includes the provider evidence.
    assert score.score == pytest.approx(0.7692307692307693)
    assert score.score <= 1.0


def test_recording_matching_when_album_artist_and_feature_suffix_differ_selects_release() -> None:
    # Given: source tags use the track artist and include a featured-credit suffix in the title.
    request = MatchingRequest(
        artist_name='Олег Груз',
        release_title='Хипхопера: Орфей & Эвридика',
        duration_seconds=160,
        album_artist_name='Noize MC',
        recording_title='Подписание контракта (Аид, Орфей, Фортуна) (feat. Noize MC & Анастасия Александрина)',
        track_number=13,
        track_total=30,
        disc_number=1,
        disc_total=1,
    )
    candidate = ReleaseCandidate(
        'release-noize',
        'Хипхопера: Орфей & Эвридика',
        'Олег Груз',
        160,
        recording_title='Подписание контракта (Аид, Орфей, Фортуна)',
        track_number=13,
        track_total=30,
        disc_number=1,
        disc_total=1,
        release_artist_name='Noize MC',
        recording_artist_names=('Олег Груз', 'Noize MC', 'Анастасия Александрина'),
    )

    # When: the release is scored against its album artist and fuzzy recording title.
    # Then: the feature suffix does not affect the release score components.
    score = score_release_candidate(request, candidate)
    assert score.score == 1.0
    assert score.artist_component == pytest.approx(4 / 15)
    assert score.release_component == pytest.approx(4 / 15)
    assert score.duration_component == pytest.approx(2 / 15)
    assert score.track_component == pytest.approx(5 / 15)
    assert score.weight == 15


def test_release_score_penalizes_track_position_mismatch_without_overriding_identity() -> None:
    # Given: local identity matches exactly, but the candidate has a different track number.
    request = MatchingRequest(
        'Fixture Artist',
        'Fixture Album',
        240,
        recording_title='Fixture Track',
        track_number=1,
        track_total=10,
        disc_number=1,
        disc_total=1,
    )
    candidate = ReleaseCandidate(
        RELEASE_MBID,
        'Fixture Album',
        'Fixture Artist',
        240,
        ('recording-id',),
        recording_title='Fixture Track',
        track_number=6,
        track_total=10,
        disc_number=1,
        disc_total=1,
    )

    # When: the release candidate is scored with all comparable position metadata.
    score = score_release_candidate(request, candidate)

    # Then: the mismatched track number lowers the score while the other position matches contribute.
    assert score.score == pytest.approx(13 / 15)
    assert score.track_component == pytest.approx(3 / 15)
    assert score.weight == 15


def test_recording_score_ignores_release_track_position() -> None:
    # Given: the source track metadata disagrees with MusicBrainz only on release track position.
    request = MatchingRequest(
        'Fixture Artist',
        'Fixture Album',
        240,
        recording_title='Fixture Track',
        track_number=1,
    )
    candidate = ReleaseCandidate(
        RELEASE_MBID,
        'Fixture Album',
        'Fixture Artist',
        240,
        ('recording-id',),
        recording_title='Fixture Track',
        track_number=6,
        release_artist_name='Fixture Artist',
        recording_artist_names=('Fixture Artist',),
    )

    # When: the recording candidate is scored component by component.
    score = score_recording_candidate(request, candidate)

    # Then: recording score uses only artist, title, and duration evidence.
    assert score.score == 1.0
    assert score.title_component == 0.4
    assert score.artist_component == 0.4
    assert score.duration_component == 0.2
    assert score.track_component == 0.0
