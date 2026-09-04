from __future__ import annotations

import json
from datetime import UTC, datetime
from itertools import permutations
from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.library.service import persist_effective_source_decision, reevaluate_effective_source_decision
from music_ingest.models import (
    Base,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    LibraryEventRecord,
    LibraryRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.quality_policy import (
    POLICY_VERSION,
    Codec,
    DecisionReason,
    ExistingDecision,
    QualityCandidate,
    evaluate,
)


def _candidate(
    source_id: str,
    codec: Codec | str,
    *,
    bit_depth: object | None = None,
    sample_rate: object | None = 44_100,
    channels: object | None = 2,
    bitrate: object | None = None,
    confirmed: bool = True,
    intake_state: str = 'present',
    disappeared: bool = False,
) -> QualityCandidate:
    return QualityCandidate(
        source_id=source_id,
        codec=codec,
        bit_depth=bit_depth,
        sample_rate=sample_rate,
        channels=channels,
        bitrate=bitrate,
        confirmed=confirmed,
        intake_state=intake_state,
        disappeared=disappeared,
    )


@pytest.mark.parametrize(
    ('candidate', 'expected_tuple'),
    (
        (_candidate('flac-24-96', Codec.FLAC, bit_depth=24, sample_rate=96_000), (1, 2, 5, 24, 96_000, 2, 0)),
        (_candidate('alac-24-96', Codec.ALAC, bit_depth=24, sample_rate=96_000), (1, 2, 4, 24, 96_000, 2, 0)),
        (_candidate('opus-320', Codec.OPUS, bitrate=320_000), (1, 1, 3, 0, 44_100, 2, 320_000)),
        (_candidate('aac-320', Codec.AAC, bitrate=320_000), (1, 1, 2, 0, 44_100, 2, 320_000)),
        (_candidate('mp3-320', Codec.MP3, bitrate=320_000), (1, 1, 1, 0, 44_100, 2, 320_000)),
        (_candidate('vorbis-320', Codec.VORBIS, bitrate=320_000), (1, 1, 0, 0, 44_100, 2, 320_000)),
    ),
)
def test_quality_policy_v1_when_supported_fixture_is_eligible_persists_complete_tuple(
    candidate: QualityCandidate, expected_tuple: tuple[int, ...]
) -> None:
    # Given: one confirmed source with measured technical facts.
    # When: the policy evaluates it.
    decision = evaluate((candidate,))

    # Then: its complete descending quality tuple is selected.
    assert decision.source_id == candidate.source_id
    assert decision.quality_tuple == expected_tuple


def test_automatic_recording_match_when_reevaluated_selects_the_source_for_publication(tmp_path: Path) -> None:
    # Given: a source automatically associated with its record and no manual review decision.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "automatic-effective-source.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 13, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(
            id='record-automatic',
            musicbrainz_recording_id='recording-mbid',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source-automatic',
            source_path='/incoming/automatic.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            media_codec='FLAC',
            media_bit_depth=16,
            media_sample_rate=44_100,
            media_channels=2,
            media_bitrate=None,
            library_record=record,
        )
        session.add_all((record, source))
        session.commit()

        # When: the publication source policy is reevaluated.
        decision = reevaluate_effective_source_decision(session, record.id, now)

        # Then: automatic provider confirmation makes the source publication-eligible.
        assert decision.source_id == source.id


def test_quality_policy_v1_when_named_fixture_matrix_is_compared_selects_flac_24_96() -> None:
    # Given: the documented cross-codec fixture matrix.
    fixtures = (
        _candidate('flac-24-96', Codec.FLAC, bit_depth=24, sample_rate=96_000),
        _candidate('flac-16-44', Codec.FLAC, bit_depth=16),
        _candidate('alac-24-96', Codec.ALAC, bit_depth=24, sample_rate=96_000),
        _candidate('opus-320', Codec.OPUS, bitrate=320_000),
        _candidate('mp3-320', Codec.MP3, bitrate=320_000),
    )

    # When: every permutation is evaluated as a retry-equivalent candidate set.
    winners = {evaluate(order).source_id for order in permutations(fixtures)}

    # Then: ordering cannot affect the lossless FLAC winner.
    assert winners == {'flac-24-96'}


@pytest.mark.parametrize(
    'candidate',
    (
        _candidate('unconfirmed', Codec.FLAC, bit_depth=24, confirmed=False),
        _candidate('disappeared', Codec.FLAC, bit_depth=24, disappeared=True),
        _candidate('quarantined', Codec.FLAC, bit_depth=24, intake_state='quarantined'),
        _candidate('unsupported', Codec.FLAC, bit_depth=24, intake_state='unsupported'),
        _candidate('failed', Codec.FLAC, bit_depth=24, intake_state='failed'),
        _candidate('unknown-codec', 'WAV', bit_depth=24),
        _candidate('missing-depth', Codec.FLAC),
        _candidate('lossless-bitrate', Codec.FLAC, bit_depth=24, bitrate=1),
        _candidate('missing-bitrate', Codec.MP3),
        _candidate('lossy-depth', Codec.MP3, bit_depth=16, bitrate=320_000),
        _candidate('text-metadata', Codec.MP3, sample_rate='44100', bitrate='320000'),
    ),
)
def test_quality_policy_v1_when_candidate_is_ineligible_excludes_it(candidate: QualityCandidate) -> None:
    # Given: one malformed or unavailable candidate.
    # When: the policy evaluates it.
    decision = evaluate((candidate,))

    # Then: it cannot become an effective source.
    assert decision.source_id is None
    assert decision.quality_tuple is None
    assert decision.reason is DecisionReason.NO_ELIGIBLE_SOURCE


@pytest.mark.parametrize(
    ('candidates', 'winner'),
    (
        (
            (_candidate('depth-16', Codec.FLAC, bit_depth=16), _candidate('depth-24', Codec.FLAC, bit_depth=24)),
            'depth-24',
        ),
        (
            (
                _candidate('rate-44', Codec.FLAC, bit_depth=24, sample_rate=44_100),
                _candidate('rate-96', Codec.FLAC, bit_depth=24, sample_rate=96_000),
            ),
            'rate-96',
        ),
        (
            (
                _candidate('mono', Codec.FLAC, bit_depth=24, channels=1),
                _candidate('stereo', Codec.FLAC, bit_depth=24, channels=2),
            ),
            'stereo',
        ),
        (
            (_candidate('mp3-128', Codec.MP3, bitrate=128_000), _candidate('mp3-320', Codec.MP3, bitrate=320_000)),
            'mp3-320',
        ),
    ),
)
def test_quality_policy_v1_when_one_tuple_dimension_is_higher_selects_that_source(
    candidates: tuple[QualityCandidate, QualityCandidate], winner: str
) -> None:
    assert evaluate(candidates).source_id == winner


def test_quality_policy_v1_when_tuples_tie_uses_ascending_immutable_source_id() -> None:
    # Given: equivalent sources whose presentation paths disagree with their IDs.
    first = _candidate('source-a', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    second = _candidate('source-z', Codec.FLAC, bit_depth=24, sample_rate=96_000)

    # When: the candidates are evaluated.
    decision = evaluate((second, first))

    # Then: only the immutable ID breaks the tie.
    assert decision.source_id == 'source-a'


@pytest.mark.parametrize('state', ('disappeared', 'quarantined', 'unsupported', 'failed'))
def test_quality_policy_v1_when_current_source_becomes_ineligible_selects_equal_eligible_replacement(
    state: str,
) -> None:
    # Given: an automatic selected source and an equal quality replacement.
    current = _candidate('source-a', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    replacement = _candidate('source-b', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    previous = ExistingDecision.from_decision(evaluate((current,)))
    unavailable = _candidate(
        'source-a',
        Codec.FLAC,
        bit_depth=24,
        sample_rate=96_000,
        intake_state=state,
        disappeared=state == 'disappeared',
    )

    # When: lifecycle data excludes the current source.
    decision = evaluate((unavailable, replacement), previous=previous)

    # Then: the equal eligible replacement becomes the effective source.
    assert decision.source_id == 'source-b'


def test_quality_policy_v1_when_manual_baseline_has_no_candidate_set_change_does_not_churn() -> None:
    # Given: a reviewer baseline.
    manual = _candidate('manual', Codec.MP3, bitrate=320_000)
    baseline = evaluate((manual,), manual_source_id='manual')
    previous = ExistingDecision.from_decision(baseline)

    # When: a retry replays the same candidate set.
    decision = evaluate((manual,), previous=previous)

    # Then: the baseline remains authoritative.
    assert decision == baseline


def test_quality_policy_v1_when_manual_baseline_candidate_set_changes_replaces_only_strictly_better() -> None:
    # Given: a manual MP3 baseline and both lower and higher new candidates.
    manual = _candidate('manual', Codec.MP3, bitrate=320_000)
    lower = _candidate('lower', Codec.MP3, bitrate=128_000)
    better = _candidate('better', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    previous = ExistingDecision.from_decision(evaluate((manual,), manual_source_id='manual'))

    # When: the candidate set changes first with a lower source, then a better source.
    lower_result = evaluate((manual, lower), previous=previous)
    better_result = evaluate((manual, better), previous=previous)

    # Then: only the strictly better candidate can replace the baseline.
    assert lower_result.source_id == 'manual'
    assert lower_result.reason is DecisionReason.MANUAL_BASELINE_RETAINED
    assert better_result.source_id == 'better'
    assert better_result.reason is DecisionReason.STRICTLY_BETTER_REPLACEMENT


def test_quality_policy_v1_when_policy_version_changes_requires_review_without_replacing() -> None:
    # Given: a persisted automatic decision under an older version.
    current = _candidate('current', Codec.MP3, bitrate=320_000)
    better = _candidate('better', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    initial = evaluate((current,))
    previous = ExistingDecision.from_decision(initial, policy_version='quality-policy-v0')

    # When: v1 sees a better eligible candidate.
    decision = evaluate((current, better), previous=previous)

    # Then: migration requests review instead of bulk replacement.
    assert decision.source_id == 'current'
    assert decision.reason is DecisionReason.POLICY_VERSION_REVIEW
    assert decision.policy_version == POLICY_VERSION


def test_effective_source_service_persists_tuple_baseline_reason_and_version_across_retries(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "quality-policy.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 11, tzinfo=UTC)
    manual = _candidate('manual', Codec.MP3, bitrate=320_000)
    better = _candidate('better', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    with Session(engine) as session:
        record = LibraryRecord(id='record-quality', created_at=now, updated_at=now)
        session.add_all((_source('manual', record), _source('better', record), record))
        session.commit()

        baseline = persist_effective_source_decision(session, record.id, (manual,), now, manual_source_id='manual')
        session.commit()
        replacement = persist_effective_source_decision(session, record.id, (manual, better), now)
        session.commit()
        retry = persist_effective_source_decision(session, record.id, (better, manual), now)
        session.commit()

        persisted = session.get(EffectiveSourceDecisionRecord, record.id)
        events = tuple(session.scalars(select(LibraryEventRecord)).all())

    assert baseline.source_id == 'manual'
    assert replacement.source_id == 'better'
    assert retry == replacement
    assert persisted is not None
    assert persisted.source_id == 'better'
    assert persisted.baseline_source_id == 'manual'
    assert persisted.policy_version == POLICY_VERSION
    assert persisted.quality_tuple_json == '[1,2,5,24,96000,2,0]'
    assert json.loads(persisted.reason)['code'] == DecisionReason.STRICTLY_BETTER_REPLACEMENT
    assert events == ()


def test_effective_source_decision_when_orphan_record_is_deleted_is_deleted_with_its_record(tmp_path: Path) -> None:
    # Given: a temporary library record with a persisted effective-source decision.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "orphan-decision.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 4, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='orphan-record', created_at=now, updated_at=now)
        source = _source('orphan-source', record)
        session.add_all((record, source))
        session.commit()
        _ = persist_effective_source_decision(
            session,
            record.id,
            (_candidate(source.id, Codec.MP3, bitrate=320_000),),
            now,
        )
        session.commit()

        # When: changed-source recovery removes the temporary record.
        session.delete(record)
        session.commit()

        # Then: its primary-key decision row is deleted rather than disassociated.
        assert session.get(EffectiveSourceDecisionRecord, record.id) is None


def test_effective_source_service_policy_migration_persists_review_event_without_replacement(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "quality-migration.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 11, tzinfo=UTC)
    current = _candidate('current', Codec.MP3, bitrate=320_000)
    better = _candidate('better', Codec.FLAC, bit_depth=24, sample_rate=96_000)
    with Session(engine) as session:
        record = LibraryRecord(id='record-migration', created_at=now, updated_at=now)
        session.add_all((_source('current', record), _source('better', record), record))
        session.commit()
        _ = persist_effective_source_decision(session, record.id, (current,), now)
        persisted = session.get(EffectiveSourceDecisionRecord, record.id)
        assert persisted is not None
        persisted.policy_version = 'quality-policy-v0'
        session.commit()

        migrated = persist_effective_source_decision(session, record.id, (current, better), now)
        session.commit()
        event = session.scalar(select(LibraryEventRecord))

    assert migrated.source_id == 'current'
    assert migrated.reason is DecisionReason.POLICY_VERSION_REVIEW
    assert event is not None
    assert event.kind == 'quality_policy_review_needed'


def test_effective_source_service_when_current_source_disappears_replaces_it_from_persisted_media_facts(
    tmp_path: Path,
) -> None:
    # Given: two confirmed equal FLAC sources with measured stream facts.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "quality-lifecycle.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 11, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-lifecycle', created_at=now, updated_at=now)
        first = _source('source-a', record)
        second = _source('source-b', record)
        for source in (first, second):
            source.media_codec = 'FLAC'
            source.media_bit_depth = 24
            source.media_sample_rate = 96_000
            source.media_channels = 2
            source.review_decisions.append(ReviewDecisionRecord(state='confirmed', rationale='fixture'))
        session.add_all((record, first, second))
        session.commit()
        initial = reevaluate_effective_source_decision(session, record.id, now)
        first.intake_state = 'disappeared'
        first.disappeared_at = now

        # When: the lifecycle re-evaluates after the selected source disappears.
        replacement = reevaluate_effective_source_decision(session, record.id, now)
        session.commit()

        # Then: persisted media facts select the equal eligible replacement.
        persisted = session.get(EffectiveSourceDecisionRecord, record.id)

    assert initial.source_id == 'source-a'
    assert replacement.source_id == 'source-b'
    assert persisted is not None
    assert persisted.source_id == 'source-b'


def test_library_api_when_confirming_source_persists_effective_decision_from_measured_facts(tmp_path: Path) -> None:
    # Given: a source with authoritative FLAC stream facts and a selectable provider result.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "quality-api.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 11, tzinfo=UTC)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    source_path = incoming / 'source-api-quality.flac'
    _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='incoming',
            canonical_path=str(incoming),
            enabled=True,
            scan_state='scanned',
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(id='record-api-quality', created_at=now, updated_at=now)
        source = _source('source-api-quality', record)
        source.source_path = str(source_path)
        source.source_root = root
        source.media_codec = 'FLAC'
        source.media_bit_depth = 24
        source.media_sample_rate = 96_000
        source.media_channels = 2
        source.candidates.append(
            CandidateRecord(
                candidate_key='release-id:track-id',
                evidence=''.join(
                    (
                        '{"provider":"musicbrainz","entity":"recording_release","release_mbid":"release-id",',
                        '"recording_mbid":"track-id","tags":{"TITLE":"Fixture","MUSICBRAINZ_ALBUMID":"release-id",',
                        '"MUSICBRAINZ_RECORDINGID":"track-id"}}',
                    )
                ),
            )
        )
        session.add_all((root, record, source))
        session.commit()

    # When: the production candidate-confirmation API is called.
    response = TestClient(create_app(lambda: Session(engine))).post(
        '/api/library/records/record-api-quality/sources/source-api-quality/candidates/select',
        json={'candidate_key': 'release-id:track-id', 'entity': 'recording_release'},
    )

    # Then: the shared policy persists the selected measured source.
    with Session(engine) as session:
        persisted = session.get(EffectiveSourceDecisionRecord, 'record-api-quality')
    assert response.status_code == 200
    assert persisted is not None
    assert persisted.source_id == 'source-api-quality'
    assert persisted.quality_tuple_json == '[1,2,5,24,96000,2,0]'


def _source(source_id: str, record: LibraryRecord) -> SourceRecord:
    return SourceRecord(
        id=source_id,
        source_path=f'/incoming/{source_id}',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='0' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        library_record=record,
    )
