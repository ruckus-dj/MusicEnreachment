from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from music_ingest.external.acoustid import AcoustIdV2Adapter
from music_ingest.matching.evidence import ProviderEvidenceResult
from music_ingest.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    FixtureCase,
    LiveProvenance,
    MusicBrainzHttpResponse,
    MusicBrainzMatch,
    RecordingCandidate,
    ReleaseCandidate,
)
from music_ingest.matching.scoring import CandidateScore, MatchDecision, MatchingRequest, resolve_match
from music_ingest.processing import worker as processing

NOW = datetime(2026, 8, 13, tzinfo=UTC)
FIXTURE = Path(__file__).parent / 'fixtures' / 'acoustid' / 'noize-pesnya-dlya-radio.json'
VOL_1_RECORDING = '47d13484-9eed-4460-babd-bca3a19fcd77'
VOL_2_RECORDING = '48c984ee-2333-442b-9483-f091162f2a62'


class NoizeAcoustIdTransport:
    def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
        _ = url, headers
        return MusicBrainzHttpResponse(200, _noize_acoustid_response())


def _noize_acoustid_response() -> bytes:
    return FIXTURE.read_bytes().rstrip(b'\n')


def _provider_result(candidate: ReleaseCandidate) -> ProviderEvidenceResult:
    body = candidate.release_mbid.encode()
    provenance = LiveProvenance(
        'musicbrainz',
        sha256(body).hexdigest(),
        sha256(body).hexdigest(),
        200,
        NOW,
        'fresh',
        body,
    )
    return ProviderEvidenceResult(MusicBrainzMatch(provenance, candidate), None)


def test_noize_acoustid_fixture_preserves_current_provider_recordings() -> None:
    # Given: the verbatim current AcoustID response captured for the Noize MC source.
    adapter = AcoustIdV2Adapter(NoizeAcoustIdTransport(), 'test-client')

    # When: the production adapter parses the mocked provider response.
    result = adapter.lookup(AcoustIdLookupRequest('noize-fingerprint', FixtureCase.SUCCESS, 173), NOW)

    # Then: it retains both current recording candidates and their shared score.
    assert sha256(_noize_acoustid_response()).hexdigest() == (
        '048562094731e7993eeb78a4cae77adb552c8a54f01b2af08d43e2902efee3a0'
    )
    assert isinstance(result, AcoustIdMatch)
    assert result.evidence.candidates == (
        RecordingCandidate(VOL_1_RECORDING, 0.96927744),
        RecordingCandidate(VOL_2_RECORDING, 0.96927744),
    )


def test_noize_vol_1_album_match_selects_its_recording_despite_equal_acoustid_scores() -> None:
    # Given: current Noize source tags and two recording lookups derived from current provider evidence.
    request = MatchingRequest(
        artist_name='Noize MC',
        release_title='The Greatest Hits Vol.1',
        duration_seconds=None,
        recording_title='Песня Для Радио',
        track_number=1,
    )
    vol_1_result = _provider_result(
        ReleaseCandidate(
            'd5c9ba44-448a-4b07-9f06-e6626032c19d',
            'The Greatest Hits Vol.1',
            'Noize MC',
            recording_mbids=(VOL_1_RECORDING,),
            recording_title='Песня для радио',
            track_number=1,
            release_artist_name='Noize MC',
        )
    )
    vol_2_result = _provider_result(
        ReleaseCandidate(
            '18a78523-16e1-45e6-91cb-594bacc9ef9b',
            'The Greatest Hits Vol.2',
            'Noize MC',
            recording_mbids=(VOL_2_RECORDING,),
            recording_title='Песня для радио (полная версия)',
            track_number=1,
            release_artist_name='Noize MC',
        )
    )
    vol_1_match = resolve_match(request, vol_1_result.musicbrainz, None)
    vol_2_match = resolve_match(request, vol_2_result.musicbrainz, None)

    # When: the worker combines the equal-score AcoustID candidates with their MusicBrainz evidence.
    selected = processing.select_acoustid_recording_match(
        ((vol_1_result, vol_1_match), (vol_2_result, vol_2_match)),
        (
            (vol_1_result, CandidateScore(VOL_1_RECORDING, 0.96927744)),
            (vol_2_result, CandidateScore(VOL_2_RECORDING, 0.96927744)),
        ),
        0.7,
    )

    # Then: the album-compatible Vol.1 recording is selected, not left in review or replaced by Vol.2.
    assert vol_1_match.decision is MatchDecision.AUTO_SELECTED
    assert vol_2_match.decision is MatchDecision.NEEDS_REVIEW
    assert selected == (vol_1_result, CandidateScore(VOL_1_RECORDING, 0.96927744))
