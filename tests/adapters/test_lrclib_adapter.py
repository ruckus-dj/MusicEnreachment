from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import UTC, datetime
from hashlib import sha256
from typing import assert_never
from urllib.parse import parse_qs, urlsplit

import pytest

from music_ingest.adapters.external.lrclib import (
    DEFAULT_USER_AGENT,
    LrclibAdapter,
    LrclibHttpResponse,
    LrclibLookupRequest,
    LrclibNoCandidate,
    LrclibProviderError,
    LrclibResult,
    LrclibSettings,
    LrclibSettingsHolder,
    LrclibSynced,
)

NOW = datetime(2026, 9, 11, tzinfo=UTC)
ENDPOINT = 'https://lrclib.net/api/search'
SYNCED_LYRICS = '[00:01.00]First line\n[00:05.00]Second line\n'

SUCCESS_REQUEST = LrclibLookupRequest(
    track_name='Fixture Track',
    artist_name='Fixture Artist',
    duration_seconds=232,
    album_name='Fixture Album',
)


def record_payload(**overrides: object) -> dict[str, object]:
    payload: dict[str, object] = {
        'id': 4242,
        'trackName': 'Fixture Track',
        'artistName': 'Fixture Artist',
        'albumName': 'Fixture Album',
        'duration': 232.0,
        'instrumental': False,
        'plainLyrics': 'plain lyrics must never be selected',
        'syncedLyrics': SYNCED_LYRICS,
    }
    payload.update(overrides)
    return payload


def search_response(*records: dict[str, object]) -> LrclibHttpResponse:
    return LrclibHttpResponse(status_code=200, body=json.dumps(list(records)).encode())


@dataclass(slots=True)
class RecordingTransport:
    response: LrclibHttpResponse
    calls: list[tuple[str, dict[str, str]]] = field(default_factory=list)

    def get(self, url: str, *, headers: dict[str, str]) -> LrclibHttpResponse:
        self.calls.append((url, headers))
        return self.response


def lookup(
    response: LrclibHttpResponse,
    *,
    settings: LrclibSettings | None = None,
    request: LrclibLookupRequest = SUCCESS_REQUEST,
) -> LrclibResult:
    transport = RecordingTransport(response)
    holder = LrclibSettingsHolder(settings)
    return LrclibAdapter(transport, holder).lookup(request, now=NOW)


def test_search_uses_structured_metadata_without_a_duration_query_parameter() -> None:
    transport = RecordingTransport(search_response(record_payload()))

    result = LrclibAdapter(transport).lookup(SUCCESS_REQUEST, now=NOW)

    assert isinstance(result, LrclibSynced)
    assert len(transport.calls) == 1
    url, headers = transport.calls[0]
    parsed = urlsplit(url)
    assert f'{parsed.scheme}://{parsed.netloc}{parsed.path}' == ENDPOINT
    assert parse_qs(parsed.query) == {
        'artist_name': ['Fixture Artist'],
        'track_name': ['Fixture Track'],
        'album_name': ['Fixture Album'],
    }
    assert headers == {'User-Agent': DEFAULT_USER_AGENT, 'Accept': 'application/json'}


def test_search_selects_the_best_synced_candidate_after_filtering_duration() -> None:
    plain_exact = record_payload(id=10, syncedLyrics=None)
    wrong_duration = record_payload(id=11, albumName='Fixture Album (Deluxe)', duration=240.0)
    deluxe_synced = record_payload(id=12, albumName='Fixture Album (Deluxe)', duration=232.0)

    result = lookup(search_response(plain_exact, wrong_duration, deluxe_synced))

    assert isinstance(result, LrclibSynced)
    assert result.provenance.provider_record_id == 12
    assert result.synced_lyrics == SYNCED_LYRICS


def test_search_accepts_a_synced_record_marked_instrumental() -> None:
    result = lookup(search_response(record_payload(instrumental=True)))

    assert isinstance(result, LrclibSynced)


def test_search_uses_smallest_lrclib_id_as_a_stable_equal_score_tie_breaker() -> None:
    high_id = record_payload(id=200)
    low_id = record_payload(id=100)

    result = lookup(search_response(high_id, low_id))

    assert isinstance(result, LrclibSynced)
    assert result.provenance.provider_record_id == 100


def test_search_rejects_the_best_candidate_below_the_configured_threshold() -> None:
    weak = record_payload(trackName='Other Track', artistName='Other Artist', albumName='Other Album')

    result = lookup(search_response(weak), settings=LrclibSettings(match_confidence_threshold=0.7))

    assert isinstance(result, LrclibNoCandidate)
    assert result.reason == 'best lrclib synced candidate is below the managed match confidence threshold'


@pytest.mark.parametrize('duration', [None, 229.9, 234.1])
def test_search_rejects_candidates_outside_the_hard_duration_gate(duration: float | None) -> None:
    result = lookup(search_response(record_payload(duration=duration)))

    assert isinstance(result, LrclibNoCandidate)
    assert result.reason == 'lrclib search returned no synced candidate matching the published duration'


def test_search_returns_no_candidate_when_no_record_has_synced_lyrics() -> None:
    result = lookup(search_response(record_payload(id=1, syncedLyrics=None), record_payload(id=2, syncedLyrics='')))

    assert isinstance(result, LrclibNoCandidate)
    assert result.reason == 'lrclib search returned no synced candidate matching the published duration'


@pytest.mark.parametrize('body', [b'{not json', b'', b'{"id": "not-an-integer"}'])
def test_search_rejects_malformed_provider_responses(body: bytes) -> None:
    result = lookup(LrclibHttpResponse(status_code=200, body=body))

    assert isinstance(result, LrclibProviderError)
    assert result.reason == 'lrclib response was malformed'


def test_provenance_excludes_lyrics_and_records_the_selected_provider_id() -> None:
    response = search_response(record_payload(id=99, plainLyrics='private plain text', syncedLyrics=SYNCED_LYRICS))

    result = lookup(response)

    assert isinstance(result, LrclibSynced)
    assert result.provenance.provider_record_id == 99
    assert result.provenance.endpoint == ENDPOINT
    assert result.provenance.response_sha256 == sha256(response.body).hexdigest()
    assert 'private plain text' not in repr(result.provenance)
    assert 'First line' not in repr(result.provenance)


def test_runtime_settings_holder_applies_a_new_search_threshold_without_rebuilding_the_adapter() -> None:
    transport = RecordingTransport(search_response(record_payload(trackName='Other Track', artistName='Other Artist')))
    holder = LrclibSettingsHolder(LrclibSettings(match_confidence_threshold=0.0))
    adapter = LrclibAdapter(transport, holder)

    accepted = adapter.lookup(SUCCESS_REQUEST, now=NOW)
    holder.update(LrclibSettings(match_confidence_threshold=0.99))
    rejected = adapter.lookup(SUCCESS_REQUEST, now=NOW)

    assert isinstance(accepted, LrclibSynced)
    assert isinstance(rejected, LrclibNoCandidate)


def test_result_union_remains_exhaustive() -> None:
    result = lookup(search_response(record_payload()))
    match result:
        case LrclibSynced() | LrclibNoCandidate() | LrclibProviderError():
            pass
        case unreachable:
            assert_never(unreachable)
