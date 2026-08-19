from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from music_ingest.matching.providers import (
    AcoustIdMatch,
    Ambiguous,
    FixtureCase,
    LiveProvenance,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    RecordingEvidence,
    ReleaseCandidate,
    Unavailable,
    release_display_title,
)
from music_ingest.matching.scoring import (
    CandidateScore,
    ExplicitMusicBrainzIds,
    LidarrContext,
    MatchDecision,
    MatchingRequest,
    ReviewReason,
    recording_candidate_matches,
    resolve_match,
    score_recording_candidate,
    select_folder_release,
)
from tests.support.providers import MusicBrainzFixtureProvider

NOW = datetime(2026, 7, 28, tzinfo=UTC)
RELEASE_MBID = '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'
RECORDING_MBID = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'


def test_matching_when_verified_explicit_release_mbid_matches_fresh_musicbrainz_selects_deterministically() -> None:
    # Given: a reviewer-supplied release ID confirmed by fresh MusicBrainz facts.
    request = MatchingRequest(
        artist_name='Fixture Artist',
        release_title='Fixture Release',
        duration_seconds=240,
        explicit_ids=ExplicitMusicBrainzIds(release_mbid=RELEASE_MBID),
        lidarr=LidarrContext('Fixture Artist', 'Fixture Release', 240),
    )
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist', 240)
    )

    # When: deterministic matching evaluates the authoritative release candidate.
    result = resolve_match(request, evidence, None)

    # Then: the release is selected from verified MusicBrainz evidence, not source tags.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == RELEASE_MBID
    assert result.recording_score.score == 0.0
    assert result.release_score.score == 1.0


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


def test_matching_when_fresh_musicbrainz_maps_explicit_recording_mbid_selects_its_release() -> None:
    # Given: a supplied recording ID mapped to one release by fresh MusicBrainz evidence.
    request = MatchingRequest(
        'Unrelated Source Artist',
        'Unrelated Source Release',
        None,
        explicit_ids=ExplicitMusicBrainzIds(recording_mbid=RECORDING_MBID),
    )
    evidence = MusicBrainzMatch(
        _provenance('fresh'),
        ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist', None, (RECORDING_MBID,)),
    )

    # When: matching resolves the authoritative recording-to-release mapping.
    result = resolve_match(request, evidence, None)

    # Then: the verified recording ID takes priority over unrelated source text.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == RELEASE_MBID
    assert result.release_score.score == 1.0


def test_matching_when_fresh_musicbrainz_maps_explicit_track_mbid_selects_its_release() -> None:
    # Given: a supplied track ID mapped to one release by fresh MusicBrainz evidence.
    track_mbid = 'd51c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    request = MatchingRequest(
        'Unrelated Source Artist',
        'Unrelated Source Release',
        None,
        explicit_ids=ExplicitMusicBrainzIds(track_mbid=track_mbid),
    )
    evidence = MusicBrainzMatch(
        _provenance('fresh'),
        ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist', None, (), (track_mbid,)),
    )

    # When: matching resolves the authoritative track-to-release mapping.
    result = resolve_match(request, evidence, None)

    # Then: the verified track ID takes priority over unrelated source text.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == RELEASE_MBID
    assert result.release_score.score == 1.0


def test_matching_when_fresh_musicbrainz_lacks_explicit_recording_mapping_keeps_manual_id_in_review() -> None:
    # Given: a reviewer-supplied recording ID absent from the authoritative release mapping.
    request = MatchingRequest(
        'Fixture Artist',
        'Fixture Release',
        None,
        explicit_ids=ExplicitMusicBrainzIds(recording_mbid=RECORDING_MBID),
    )
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist')
    )

    # When: matching cannot verify the proposed recording-to-release relationship.
    result = resolve_match(request, evidence, None)

    # Then: it preserves the supplied ID as a reviewer assertion instead of inventing a mapping.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason is ReviewReason.MANUAL_MBID_UNVERIFIED


def test_matching_when_source_title_contains_bidi_control_requires_review() -> None:
    # Given: a source title that would match only after a bidi control is discarded.
    request = MatchingRequest('Fixture Artist', 'Fixture \u202eRelease', None)
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist')
    )

    # When: matching evaluates untrusted source text.
    result = resolve_match(request, evidence, None)

    # Then: unsafe text cannot be normalized into an automatic canonical selection.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason is ReviewReason.UNSAFE_TEXT


def test_matching_when_fingerprint_equivalent_live_and_studio_pair_is_ambiguous_requires_review() -> None:
    # Given: a recording fingerprint and MusicBrainz ambiguity between live and studio releases.
    request = MatchingRequest('Fixture Artist', 'Fixture Release Live', 240)
    musicbrainz = Ambiguous(_provenance('fresh'))
    acoustid = AcoustIdMatch(_provenance('fresh', 'acoustid'), RecordingEvidence(RECORDING_MBID, 0.98))

    # When: matching scores the recording separately from release evidence.
    result = resolve_match(request, musicbrainz, acoustid)

    # Then: fingerprint evidence remains recording-only and cannot choose a release.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.recording_score.score == 0.98
    assert result.release_score.score == 0.0


def test_matching_when_source_album_disambiguates_musicbrainz_candidates_selects_exact_album() -> None:
    # Given: two releases for the same artist and track, with only one matching the source album tag.
    request = MatchingRequest('Noize MC', 'The Greatest Hits Vol.2', None)
    evidence = Ambiguous(
        _provenance('fresh'),
        (
            ReleaseCandidate('release-vol-1', 'The Greatest Hits Vol.1', 'Noize MC'),
            ReleaseCandidate('release-vol-2', 'The Greatest Hits Vol.2', 'Noize MC'),
        ),
    )

    # When: matching evaluates the ambiguous MusicBrainz result.
    result = resolve_match(request, evidence, None)

    # Then: the unique exact source album match is selected automatically.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == 'release-vol-2'


def test_matching_when_source_album_contains_release_disambiguation_selects_matching_edition() -> None:
    # Given: the source album includes the MusicBrainz release disambiguation in parentheses.
    request = MatchingRequest('Кис-Кис', 'Юность в стиле панк (Baby Punk Version)', 212)
    baby_punk = ReleaseCandidate(
        '3b98979b-6494-4a7c-8de6-2165902f8a87',
        'юность в стиле панк',
        'Кис-Кис',
        212,
        ('cddf7780-179f-4abe-b706-65fd10be8975',),
        disambiguation='baby punk version',
    )
    standard = ReleaseCandidate(
        '55c7242f-1b4b-485d-b5f2-d6a8feeee088',
        'юность в стиле панк',
        'Кис-Кис',
        212,
        ('5a0a6087-6be1-4833-aa3b-7b4c891a5c3a',),
    )

    # When: matching compares the complete release display title for both editions.
    result = resolve_match(request, Ambiguous(_provenance('fresh'), (baby_punk, standard)), None)

    # Then: the annotated release and its linked recording are selected together.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == baby_punk.release_mbid
    assert result.recording_score.candidate_mbid == 'cddf7780-179f-4abe-b706-65fd10be8975'
    assert release_display_title(baby_punk) == 'юность в стиле панк (baby punk version)'


def test_matching_when_one_release_has_related_recording_candidate_uses_confidence_threshold() -> None:
    # Given: MusicBrainz returns release and recording evidence for the same release,
    # while the source album contains a small title difference.
    request = MatchingRequest(
        'Кис-Кис & Turbosh',
        'ЛБТЛ (Dance Remix)',
        173,
        recording_title='ЛБТЛ (Dance Remix)',
        album_artist_name='Кис-Кис & Turbosh',
    )
    release_mbid = '588907d5-ed2a-44af-880d-f99738711f1a'
    evidence = MusicBrainzMatch(
        _provenance('fresh'),
        ReleaseCandidate(
            release_mbid,
            'лбтд (dance remix)',
            'Кис-Кис & Turbosh',
            173,
            ('0b11859c-8da5-4071-bad8-9fab85283fb2',),
            release_artist_name='Кис-Кис & Turbosh',
        ),
    )

    # When: matching resolves the related candidate set at the configured threshold.
    result = resolve_match(request, evidence, None, confidence_threshold=0.70)

    # Then: the unique release is selected despite its related recording evidence.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == release_mbid
    assert result.release_score.score >= 0.70


def test_matching_when_single_fresh_release_matches_source_artist_and_album_selects_automatically() -> None:
    # Given: MusicBrainz returns one release with an exact source artist and album match.
    request = MatchingRequest('Noize MC', 'The Greatest Hits Vol.1', None)
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate('release-vol-1', 'The Greatest Hits Vol.1', 'Noize MC')
    )

    # When: matching evaluates the verified release.
    result = resolve_match(request, evidence, None, confidence_threshold=0.9)

    # Then: exact album evidence does not remain pending merely because the duration is unavailable.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == 'release-vol-1'


def test_matching_when_musicbrainz_text_candidate_has_recording_context_selects_recording_without_acoustid() -> None:
    # Given: MusicBrainz text search returned the source track and no fingerprint evidence exists.
    request = MatchingRequest(
        'Fixture Artist',
        'Fixture Album',
        215,
        recording_title='Target Song',
        track_number=2,
    )
    candidate = ReleaseCandidate(
        RELEASE_MBID,
        'Fixture Album',
        'Fixture Artist',
        215,
        ('recording-from-search',),
        recording_title='Target Song',
        track_number=2,
        release_artist_name='Fixture Artist',
        recording_artist_names=('Fixture Artist',),
    )

    # When: matching resolves the MusicBrainz-only candidate.
    result = resolve_match(request, MusicBrainzMatch(_provenance('fresh'), candidate), None)

    # Then: recording identity comes from the ranked MusicBrainz candidate and remains high confidence.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.recording_score.candidate_mbid == 'recording-from-search'
    assert result.recording_score.score >= 0.9


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
    result = resolve_match(request, MusicBrainzMatch(_provenance('fresh'), candidate), None)

    # Then: the feature suffix does not block the unique authoritative release selection.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == 'release-noize'
    assert result.release_score.score == 1.0
    assert result.release_score.artist_component == 0.4
    assert result.release_score.release_component == 0.4
    assert result.release_score.duration_component == 0.2


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


def test_matching_when_album_artist_is_missing_falls_back_to_track_artist() -> None:
    # Given: older source metadata has no ALBUMARTIST value.
    request = MatchingRequest('Fixture Artist', 'Fixture Release', None)
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist')
    )

    # When: matching evaluates the candidate using the fallback artist.
    result = resolve_match(request, evidence, None)

    # Then: the existing exact artist behavior remains available.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == RELEASE_MBID


def test_matching_when_recording_lookup_echoes_source_artist_but_release_artist_conflicts_requires_review() -> None:
    # Given: a recording lookup carries the source artist as context, but MusicBrainz names a different release artist.
    request = MatchingRequest('Noize MC', 'The Greatest Hits Vol.1', None)
    evidence = MusicBrainzMatch(
        _provenance('fresh'),
        ReleaseCandidate(
            'release-vol-1',
            'The Greatest Hits Vol.1',
            'Noize MC',
            release_artist_name='Unrelated Artist',
        ),
    )

    # When: matching evaluates the authoritative release identity rather than echoed request context.
    result = resolve_match(request, evidence, None)

    # Then: it cannot automatically select a release whose MusicBrainz artist conflicts with the source artist.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason is ReviewReason.INSUFFICIENT_RELEASE_SCORE


def test_matching_when_source_album_matches_multiple_candidates_keeps_review() -> None:
    # Given: two candidates have identical artist and album text.
    request = MatchingRequest('Noize MC', 'The Greatest Hits Vol.2', None)
    evidence = Ambiguous(
        _provenance('fresh'),
        (
            ReleaseCandidate('release-one', 'The Greatest Hits Vol.2', 'Noize MC'),
            ReleaseCandidate('release-two', 'The Greatest Hits Vol.2', 'Noize MC'),
        ),
    )

    # When: matching evaluates the ambiguous MusicBrainz result.
    result = resolve_match(request, evidence, None)

    # Then: duplicate exact matches remain a manual review case.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.review_reason is ReviewReason.MUSICBRAINZ_AMBIGUOUS


def test_matching_when_source_catalog_selects_one_release_edition() -> None:
    # Given: the source path identifies an edition while MusicBrainz returns several same-title releases.
    request = MatchingRequest(
        'Linkin Park',
        'Burn It Down',
        230,
        source_path='/downloads/2012 - Burn It Down [EU 9362-49505-0]/01 - Burn It Down.flac',
    )
    evidence = Ambiguous(
        _provenance('fresh'),
        (
            ReleaseCandidate('release-eu', 'Burn It Down', 'Linkin Park', 230, catalog_numbers=('9362-49505-0',)),
            ReleaseCandidate('release-us', 'Burn It Down', 'Linkin Park', 230, catalog_numbers=('093624950387',)),
        ),
    )

    # When: matching evaluates the edition evidence together with source artist and album.
    result = resolve_match(request, evidence, None)

    # Then: only the release whose catalog number is present in the source path is selected.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == 'release-eu'


def test_matching_when_source_has_no_catalog_evidence_keeps_same_title_releases_in_review() -> None:
    # Given: same-title releases have catalog numbers, but the source path carries no edition identifier.
    request = MatchingRequest('Linkin Park', 'Burn It Down', 230, source_path='/downloads/Burn It Down/track.flac')
    evidence = Ambiguous(
        _provenance('fresh'),
        (
            ReleaseCandidate('release-eu', 'Burn It Down', 'Linkin Park', 230, catalog_numbers=('093624950509',)),
            ReleaseCandidate('release-us', 'Burn It Down', 'Linkin Park', 230, catalog_numbers=('093624950387',)),
        ),
    )

    # When: matching evaluates the ambiguous releases without edition evidence.
    result = resolve_match(request, evidence, None)

    # Then: it does not guess an edition from release order or score.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.review_reason is ReviewReason.MUSICBRAINZ_AMBIGUOUS


def test_matching_when_exact_musicbrainz_facts_are_freshly_cached_selects_the_release() -> None:
    # Given: an exact normalized candidate retained inside the 24-hour cache window.
    request = MatchingRequest('Fíxture Artist', 'fixture release', None)
    evidence = MusicBrainzMatch(
        _provenance('cached'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist')
    )

    # When: matching resolves the cached authority evidence.
    result = resolve_match(request, evidence, None)

    # Then: fresh cached MusicBrainz facts have enough release evidence for selection.
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == RELEASE_MBID
    assert result.release_score.score == 0.8


def test_recording_candidate_matching_rejects_acoustid_false_positive_and_selects_japanese_maxi() -> None:
    # Given: AcoustID gives the same high score to a Beatles false positive and three We Made It variants.
    request = MatchingRequest(
        artist_name='Busta Rhymes feat. Linkin Park',
        release_title='We Made It [Maxi Single]',
        duration_seconds=238,
        recording_title='We Made It (Album Version)',
        track_number=1,
        track_total=3,
        disc_number=1,
        disc_total=1,
        source_path='/downloads/Japan WPCR-12973/01 - We Made It (Album Version).flac',
    )
    candidates = (
        ReleaseCandidate(
            'beatles-release',
            'Let It Be',
            'The Beatles',
            232,
            ('c3ab18e7-e17a-4064-a352-834b67513f33',),
            recording_title='Let It Be',
            track_number=1,
            track_total=12,
        ),
        ReleaseCandidate(
            'clean-release',
            'We Made It',
            'Busta Rhymes feat. Linkin Park',
            238,
            ('6eddd1bf-2a06-4baf-8b31-0909963345c7',),
            recording_title='We Made It (amended version)',
            track_number=1,
            track_total=3,
        ),
        ReleaseCandidate(
            'instrumental-release',
            'We Made It',
            'Busta Rhymes feat. Linkin Park',
            236,
            ('fea273ef-bd0b-4f3a-ba7a-6d9ed240c2f5',),
            recording_title='We Made It (instrumental)',
            track_number=3,
            track_total=3,
        ),
        ReleaseCandidate(
            '0f481339-f7bb-40b4-ab4a-f24c1c2a7009',
            'We Made It',
            'Busta Rhymes feat. Linkin Park',
            238,
            ('5eb8e3dc-7a63-4269-9abb-a7ed70a27cf4',),
            recording_title='We Made It (album version)',
            track_number=1,
            track_total=3,
            disc_number=1,
            disc_total=1,
            country='JP',
        ),
    )

    # When: MusicBrainz facts validate each high-confidence AcoustID recording candidate.
    selected = tuple(candidate for candidate in candidates if recording_candidate_matches(request, candidate))

    # Then: only the source-consistent recording and its Japanese Maxi Single release remain eligible.
    assert tuple(candidate.recording_mbids for candidate in selected) == (('5eb8e3dc-7a63-4269-9abb-a7ed70a27cf4',),)
    assert selected[0].release_mbid == '0f481339-f7bb-40b4-ab4a-f24c1c2a7009'
    result = resolve_match(request, Ambiguous(_provenance('fresh'), candidates), None)
    assert result.decision is MatchDecision.AUTO_SELECTED
    assert result.selected_release_mbid == '0f481339-f7bb-40b4-ab4a-f24c1c2a7009'


def test_recording_candidate_matching_accepts_recording_artist_on_compilation_release() -> None:
    # Given: a compilation release has a generic release artist while the recording artist matches the source.
    request = MatchingRequest(
        artist_name='Fixture Artist',
        release_title='Fixture Compilation',
        duration_seconds=180,
        recording_title='Fixture Track',
    )
    candidate = ReleaseCandidate(
        'compilation-release',
        'Fixture Compilation',
        'Fixture Artist',
        180,
        recording_title='Fixture Track',
        release_artist_name='Various Artists',
        recording_artist_names=('Fixture Artist',),
    )

    # When: recording context is evaluated against the release and track artist credits.
    matches = recording_candidate_matches(request, candidate)

    # Then: a unique track-level artist credit keeps the recording eligible for automatic selection.
    assert matches


def test_matching_when_musicbrainz_outage_preserves_local_only_review_path() -> None:
    # Given: local metadata and an unavailable provider without a fabricated MBID.
    request = MatchingRequest('Unknown Artist', 'Historical Tape', 180, local_only=True)
    musicbrainz = Unavailable(_provenance('fresh'))

    # When: matching handles the provider outage.
    result = resolve_match(request, musicbrainz, None)

    # Then: a reviewer can make a local-only decision without canonical release metadata.
    assert result.decision is MatchDecision.LOCAL_ONLY_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason == 'local_only_requested'


def test_matching_when_manual_mbid_cannot_be_verified_routes_the_reviewer_assertion_to_review() -> None:
    # Given: a supplied release ID that MusicBrainz cannot confirm.
    request = MatchingRequest(
        'Fixture Artist',
        'Fixture Release',
        240,
        explicit_ids=ExplicitMusicBrainzIds(release_mbid=RELEASE_MBID),
    )
    evidence = MusicBrainzMatch(
        _provenance('fresh'), ReleaseCandidate('different-release', 'Fixture Release', 'Fixture Artist', 240)
    )

    # When: matching resolves the conflicting authoritative response.
    result = resolve_match(request, evidence, None)

    # Then: the manual ID is a reviewer assertion rather than canonical metadata.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason == 'manual_mbid_unverified'


def test_matching_when_musicbrainz_evidence_is_stale_requires_review_even_for_exact_text() -> None:
    # Given: an otherwise exact candidate whose retained provider evidence is stale.
    request = MatchingRequest('Fixture Artist', 'Fixture Release', 240)
    evidence = MusicBrainzMatch(
        _provenance('stale'), ReleaseCandidate(RELEASE_MBID, 'Fixture Release', 'Fixture Artist', 240)
    )

    # When: matching evaluates the stale candidate.
    result = resolve_match(request, evidence, None)

    # Then: stale evidence cannot make a release canonical.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason == 'musicbrainz_stale'


def test_matching_when_successful_fixture_evidence_is_not_live_or_fresh_requires_review() -> None:
    # Given: an apparently successful fixture result that has no live/fresh provider provenance.
    provider = MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz')
    evidence = provider.lookup(MusicBrainzLookupRequest('fixture release', FixtureCase.SUCCESS))
    request = MatchingRequest('Fixture Artist', 'Fixture Release', None)

    # When: matching receives the superficially successful provider result directly.
    result = resolve_match(request, evidence, None)

    # Then: it cannot silently treat fixture or stale data as canonical authority evidence.
    assert result.decision is MatchDecision.NEEDS_REVIEW
    assert result.selected_release_mbid is None
    assert result.review_reason == 'musicbrainz_unknown'


def _provenance(state: str, provider_name: str = 'musicbrainz') -> LiveProvenance:
    request_hash = sha256(f'{provider_name}:{state}'.encode()).hexdigest()
    return LiveProvenance(provider_name, request_hash, request_hash, 200, NOW, state)
