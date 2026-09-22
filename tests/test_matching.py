from __future__ import annotations

import pytest

from music_ingest.services.matching.musicbrainz_mapping import merge_recording_candidate
from music_ingest.services.matching.providers import ReleaseCandidate
from music_ingest.services.matching.scoring import (
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
    assert score.score == pytest.approx(0.9017241379310345)
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
    assert score.track_number_component == pytest.approx(0.0)
    assert score.track_number_match == pytest.approx(0.0)
    assert score.track_total_component == pytest.approx(1 / 15)
    assert score.track_total_match == pytest.approx(1.0)
    assert score.disc_number_component == pytest.approx(1 / 15)
    assert score.disc_total_component == pytest.approx(1 / 15)
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


def test_recording_score_includes_available_acoustid_evidence() -> None:
    # Given: text metadata has a transliteration mismatch, but AcousticID identifies the recording.
    request = MatchingRequest('кис-кис', 'Пир во время чумы', 27, recording_title='intro')
    candidate = ReleaseCandidate(
        'release-id',
        'Пир во время чумы',
        'кис-кис',
        27,
        ('recording-id',),
        recording_title='интро',
        musicbrainz_score=100,
    )

    # When: the recording candidate is scored with the matched AcousticID result.
    score = score_recording_candidate(request, candidate, acoustid_score=0.9997)

    # Then: AcousticID contributes its weighted evidence to the normalized composite score.
    assert score.score == pytest.approx((4 + 4 + 2 + 2 + 4 * 0.9997) / 16)
    assert score.weight == 16


def test_recording_score_compares_all_recording_artists() -> None:
    # Given: source and recording have the same two artists with different separators and case.
    request = MatchingRequest('Кис-Кис & Elcofff', 'Мальчик (Hardstyle Remix)', 100, recording_title='Мальчик')
    candidate = ReleaseCandidate(
        'release-id',
        'Мальчик (Hardstyle Remix)',
        'Кис-Кис',
        100,
        ('recording-id',),
        recording_title='Мальчик',
        recording_artist_names=('Кис-Кис', 'Elcofff'),
        musicbrainz_score=100,
    )

    # When: the recording candidate is scored using its complete artist credit.
    score = score_recording_candidate(request, candidate)

    # Then: the complete normalized artist credit matches exactly.
    assert score.artist_component == pytest.approx(4 / 12)
    assert score.score == pytest.approx(1.0)


def test_text_similarity_recognizes_cross_script_transliteration() -> None:
    # Given: source and candidate use Latin and Cyrillic spellings of the same word.
    request = MatchingRequest('Artist', 'Album', 100, recording_title='index')
    candidate = ReleaseCandidate(
        'release-id',
        'Album',
        'Artist',
        100,
        ('recording-id',),
        recording_title='индекс',
    )

    # When: the recording candidate is scored with both original and transliterated text.
    score = score_recording_candidate(request, candidate)

    # Then: transliterated titles receive a non-zero similarity contribution.
    assert score.title_match is not None
    assert score.title_match > 0.0


def test_recording_score_softens_extra_artist_credit_tokens() -> None:
    # Given: the candidate artist credit includes additional participants.
    request = MatchingRequest('Linkin Park', '', None)
    candidate = ReleaseCandidate(
        'release-id',
        '',
        'Linkin Park; Evidence; Pharoahe Monch; DJ Babu',
        None,
        ('recording-id',),
    )

    # When: the recording candidate is scored.
    score = score_recording_candidate(request, candidate)

    # Then: the shared artist tokens retain a review-threshold match with a coverage penalty.
    assert score.recording_artist_match == pytest.approx(0.705, abs=0.01)


@pytest.mark.parametrize(
    ('source_title', 'candidate_title', 'expected_match'),
    (
        ('H! Vltg3', 'H! VLTG3 (Single Edit)', pytest.approx(0.776, abs=0.01)),
        ('[PTS.OF.ATHRTY]', 'Mmm...Cookies: Sweet Hamster Like Jewels from America!', pytest.approx(0.184, abs=0.01)),
    ),
)
def test_recording_score_penalizes_extra_or_unrelated_title_tokens(
    source_title: str, candidate_title: str, expected_match: float
) -> None:
    # Given: a source title and its MusicBrainz candidate title.
    request = MatchingRequest('Artist', '', None, recording_title=source_title)
    candidate = ReleaseCandidate('release-id', '', 'Artist', None, ('recording-id',), recording_title=candidate_title)

    # When: the recording candidate is scored.
    score = score_recording_candidate(request, candidate)

    # Then: extra edition tokens are softly penalized and an unrelated title stays low.
    assert score.title_match == expected_match


@pytest.mark.parametrize(
    ('source_duration', 'candidate_duration', 'expected_score'),
    (
        (276, 276, 1.0),
        (278, 276, 0.783),
        (281, 276, 0.505),
        (291, 276, -0.029),
        (552, 276, -1.0),
        (1104, 276, -2.0),
    ),
)
def test_duration_score_changes_smoothly_from_close_to_relative_mismatches(
    source_duration: int, candidate_duration: int, expected_score: float
) -> None:
    # Given: source and candidate durations spanning small and multiplicative differences.

    # When: matching scores their duration evidence.
    score = score_recording_candidate(
        MatchingRequest('Fixture Artist', '', source_duration),
        ReleaseCandidate('release-id', '', 'Fixture Artist', candidate_duration, ('recording-id',)),
    )

    # Then: the score moves continuously from a close-match bonus to a multiplicative penalty.
    assert score.duration_match == pytest.approx(expected_score, abs=0.001)


def test_recording_score_clamps_a_gross_duration_mismatch_to_zero() -> None:
    # Given: an album-length source and a short track with only the artist in common.
    request = MatchingRequest('Noize MC', '', 3496)
    candidate = ReleaseCandidate('release-id', '', 'Noize MC', 276, ('recording-id',))

    # When: matching scores the recording candidate.
    score = score_recording_candidate(request, candidate)

    # Then: the negative duration factor cannot produce a negative composite score.
    assert score.duration_match == pytest.approx(-3.663, abs=0.001)
    assert score.score == 0.0
