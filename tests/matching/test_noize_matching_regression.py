from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256

import music_ingest.services.candidates as processing
from music_ingest.adapters.external.acoustid import AcoustIdV2Adapter
from music_ingest.services.matching.evidence import ProviderEvidenceResult
from music_ingest.services.matching.providers import (
    AcoustIdLookupRequest,
    AcoustIdMatch,
    FixtureCase,
    LiveProvenance,
    MusicBrainzHttpResponse,
    MusicBrainzMatch,
    RecordingCandidate,
    ReleaseCandidate,
)
from music_ingest.services.matching.scoring import CandidateScore, MatchDecision, MatchResult
from tests.support.paths import FIXTURES_DIRECTORY

NOW = datetime(2026, 8, 13, tzinfo=UTC)
FIXTURE = FIXTURES_DIRECTORY / 'acoustid' / 'noize-pesnya-dlya-radio.json'
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


def test_album_match_keeps_acoustid_recording_when_recording_projection_is_missing() -> None:
    # Given: one album-compatible MusicBrainz match still carries its verified AcousticID score.
    provider_result = _provider_result(
        ReleaseCandidate(
            'release-vol-1',
            'The Greatest Hits Vol.1',
            'Noize MC',
            recording_mbids=(VOL_1_RECORDING,),
        )
    )
    album_match = MatchResult(
        MatchDecision.AUTO_SELECTED,
        'release-vol-1',
        CandidateScore(VOL_1_RECORDING, 0.96927744),
        CandidateScore('release-vol-1', 0.8),
        None,
    )

    # When: album resolution runs without a separately projected recording row.
    selected = processing.select_acoustid_recording_match(((provider_result, album_match),), (), 0.7)

    # Then: album evidence still selects the corresponding AcousticID recording.
    assert selected == (provider_result, CandidateScore(VOL_1_RECORDING, 0.96927744))
