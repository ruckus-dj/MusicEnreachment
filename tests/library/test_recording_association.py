from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    CandidateRecord,
    JobRecord,
    LibraryMetadataRevisionRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    SourceAssociationOverrideRecord,
    SourceRecord,
)
from music_ingest.services.association import (
    AutomaticAssociationRequest,
    ManualAssociationRequest,
    RecordingAssociationService,
)
from music_ingest.services.library import append_metadata_revision
from music_ingest.services.matching.providers import (
    Ambiguous,
    FixtureProvenance,
    MusicBrainzLookupRequest,
    MusicBrainzResult,
    ReleaseCandidate,
)
from tests.support.paths import FIXTURES_DIRECTORY
from tests.support.providers import MusicBrainzFixtureProvider


def test_automatic_association_when_verified_recording_matches_groups_sources(tmp_path: Path) -> None:
    # Given: two independently attached sources with verified evidence for one recording.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "automatic-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        first = LibraryRecord(id='record-first', created_at=now, updated_at=now)
        second = LibraryRecord(id='record-second', created_at=now, updated_at=now)
        source_one = SourceRecord(
            id='source-one',
            source_path='/incoming/one.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=first,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='musicbrainzmatch', snapshot_sha256='c' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='release-one',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.98,"tags":'
                        '{"MUSICBRAINZ_ALBUMID":"release-id",'
                        '"MUSICBRAINZ_RECORDINGID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
        )
        source_two = SourceRecord(
            id='source-two',
            source_path='/incoming/two.flac',
            device=1,
            inode=2,
            size_bytes=1,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=second,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='musicbrainzmatch', snapshot_sha256='d' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='release-two',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.98,"tags":'
                        '{"MUSICBRAINZ_ALBUMID":"release-id",'
                        '"MUSICBRAINZ_RECORDINGID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
        )
        session.add_all((first, second, source_one, source_two))
        session.commit()

        # When: each source receives the same confidence-qualified verified recording identity.
        service = RecordingAssociationService(session)
        for source_id in (source_one.id, source_two.id):
            _ = service.associate_automatic(
                AutomaticAssociationRequest(
                    source_id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
                )
            )
        session.commit()

        # Then: both sources resolve to the one recording aggregate.
        first_source = session.get(SourceRecord, source_one.id)
        second_source = session.get(SourceRecord, source_two.id)
        assert first_source is not None
        assert second_source is not None
        assert first_source.library_record_id == second_source.library_record_id


def test_automatic_association_batch_when_requests_are_reversed_locks_sources_and_records_by_id(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    # Given: two source records whose request order conflicts with their stable identifiers.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "batch-association-locks.db"}', echo='debug')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        target = LibraryRecord(
            id='record-target',
            musicbrainz_recording_id=recording_mbid,
            musicbrainz_release_id=release_mbid,
            created_at=now,
            updated_at=now,
        )
        first = LibraryRecord(id='record-a', created_at=now, updated_at=now)
        second = LibraryRecord(id='record-z', created_at=now, updated_at=now)
        source_a = SourceRecord(
            id='source-a',
            source_path='/incoming/a.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=first,
        )
        source_z = SourceRecord(
            id='source-z',
            source_path='/incoming/z.flac',
            device=1,
            inode=2,
            size_bytes=1,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=second,
        )
        session.add_all((target, first, second, source_a, source_z))
        session.commit()

        # When: batch association receives the reverse source order.
        results = RecordingAssociationService(session).associate_automatic_batch(
            (
                AutomaticAssociationRequest(
                    source_z.id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
                ),
                AutomaticAssociationRequest(
                    source_a.id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
                ),
            )
        )
        session.commit()

        # Then: the result preserves request order while both lock phases are ordered by stable identifiers.
        assert tuple(result.library_record_id if result is not None else None for result in results) == (
            target.id,
            target.id,
        )
        statements = '\n'.join(record.message for record in caplog.records)
        assert 'FROM source_records' in statements
        assert 'ORDER BY source_records.id' in statements
        assert 'FROM library_records' in statements
        assert 'ORDER BY library_records.id' in statements


def test_automatic_association_batch_moves_sources_and_preserves_assignment_event_and_revision_semantics(
    tmp_path: Path,
) -> None:
    # Given: independently attached sources, including one with immutable final metadata, target one recording-release.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "batch-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        target = LibraryRecord(
            id='record-target',
            musicbrainz_recording_id=recording_mbid,
            musicbrainz_release_id=release_mbid,
            created_at=now,
            updated_at=now,
        )
        first = LibraryRecord(id='record-first', created_at=now, updated_at=now)
        second = LibraryRecord(id='record-second', created_at=now, updated_at=now)
        source_one = SourceRecord(
            id='source-one',
            source_path='/incoming/one.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=first,
        )
        source_two = SourceRecord(
            id='source-two',
            source_path='/incoming/two.flac',
            device=1,
            inode=2,
            size_bytes=1,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=second,
        )
        session.add_all((target, first, second, source_one, source_two))
        session.flush()
        _ = append_metadata_revision(session, first.id, source_one.id, 'final', {'TITLE': 'Fixture'}, 'worker', now)
        session.commit()

        # When: the qualified requests are associated as one batch.
        results = RecordingAssociationService(session).associate_automatic_batch(
            (
                AutomaticAssociationRequest(
                    source_two.id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
                ),
                AutomaticAssociationRequest(
                    source_one.id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
                ),
            )
        )
        session.commit()

        # Then: both sources move once, each reassignment is recorded, and immutable final metadata is recreated.
        persisted_one = session.get(SourceRecord, source_one.id)
        persisted_two = session.get(SourceRecord, source_two.id)
        persisted_target = session.get(LibraryRecord, target.id)
        assert all(result is not None and result.library_record_id == target.id for result in results)
        assert persisted_one is not None
        assert persisted_two is not None
        assert persisted_target is not None
        assert persisted_one.library_record_id == target.id
        assert persisted_two.library_record_id == target.id
        assert len(persisted_one.recording_assignments) == 1
        assert len(persisted_two.recording_assignments) == 1
        assert [
            (revision.source_id, revision.tags_json, revision.actor)
            for revision in persisted_target.metadata_revisions
            if revision.layer == 'final'
        ] == [(source_one.id, '{"TITLE": "Fixture"}', 'reassociation')]
        assert len(persisted_target.events) == 2


def test_automatic_association_batch_when_sources_already_target_replays_without_history(tmp_path: Path) -> None:
    # Given: a completed batch association.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "batch-association-replay.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        target = LibraryRecord(
            id='record-target',
            musicbrainz_recording_id=recording_mbid,
            musicbrainz_release_id=release_mbid,
            created_at=now,
            updated_at=now,
        )
        previous = LibraryRecord(id='record-previous', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-replay',
            source_path='/incoming/replay.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=previous,
        )
        request = AutomaticAssociationRequest(
            source.id, recording_mbid, 0.98, 0.9, '{"provider":"fixture"}', now, release_mbid
        )
        session.add_all((target, previous, source))
        session.commit()
        service = RecordingAssociationService(session)
        _ = service.associate_automatic_batch((request,))
        session.commit()
        session.expire_all()
        persisted_target = session.get(LibraryRecord, target.id)
        persisted_source = session.get(SourceRecord, source.id)
        assert persisted_target is not None
        assert persisted_source is not None
        event_count = len(persisted_target.events)
        assignment_count = len(persisted_source.recording_assignments)
        revision_count = len(persisted_target.metadata_revisions)
        for refresh_job in session.query(JobRecord).filter_by(kind='selection_refresh'):
            refresh_job.state = 'completed'
        session.commit()

        # When: the same source-target association is replayed.
        results = service.associate_automatic_batch((request,))
        session.commit()
        session.expire_all()

        # Then: it reports the resolved target without writing another assignment, event, or revision.
        replayed_target = session.get(LibraryRecord, target.id)
        replayed_source = session.get(SourceRecord, source.id)
        assert results[0] is not None
        assert results[0].library_record_id == target.id
        assert results[0].moved_from_record_id == target.id
        assert replayed_target is not None
        assert replayed_source is not None
        assert len(replayed_target.events) == event_count
        assert len(replayed_source.recording_assignments) == assignment_count
        assert len(replayed_target.metadata_revisions) == revision_count
        assert (
            session.query(JobRecord)
            .filter_by(kind='selection_refresh', library_record_id=target.id, state='queued')
            .count()
            == 1
        )


def test_library_record_metadata_revisions_order_equal_timestamps_by_revision_and_id(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "metadata-revision-order.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    with Session(engine) as session:
        record = LibraryRecord(id='record-revision-order', created_at=now, updated_at=now)
        session.add(record)
        session.flush()
        session.add_all(
            (
                LibraryMetadataRevisionRecord(
                    id=20,
                    library_record_id=record.id,
                    layer='final',
                    revision=2,
                    tags_json='{"TITLE":"Second"}',
                    actor='test',
                    created_at=now,
                ),
                LibraryMetadataRevisionRecord(
                    id=10,
                    library_record_id=record.id,
                    layer='final',
                    revision=1,
                    tags_json='{"TITLE":"First"}',
                    actor='test',
                    created_at=now,
                ),
            )
        )
        session.commit()
        session.expire_all()

        persisted = session.get(LibraryRecord, record.id)
        assert persisted is not None
        assert [(revision.revision, revision.id) for revision in persisted.metadata_revisions] == [(1, 10), (2, 20)]


def test_automatic_association_when_release_pair_is_unconfirmed_uses_the_qualified_score(tmp_path: Path) -> None:
    # Given: a high-confidence recording and release request without an extra persisted-pair guard.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "ambiguous-release-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    with Session(engine) as session:
        record = LibraryRecord(id='record-ambiguous-release', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-ambiguous-release',
            source_path='/incoming/ambiguous-release.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='ambiguous', snapshot_sha256='b' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='album-compatible-release',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.98,"tags":'
                        '{"MUSICBRAINZ_RECORDINGID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
        )
        session.add_all((record, source))
        session.commit()

        # When: automatic association receives a confidence-qualified recording and release.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(
                source.id, recording_mbid, 0.98, 0.9, '{"provider":"worker"}', now, 'release-id'
            )
        )
        session.commit()

        # Then: the source moves to the exact recording-release identity selected by scoring.
        persisted = session.get(SourceRecord, source.id)
        assert persisted is not None
        assert result is not None
        assert persisted.library_record_id == result.library_record_id
        target = session.get(LibraryRecord, result.library_record_id)
        assert target is not None
        assert target.musicbrainz_recording_id == recording_mbid
        assert target.musicbrainz_release_id == 'release-id'


def test_automatic_association_when_source_has_final_metadata_recreates_it_on_target(tmp_path: Path) -> None:
    # Given: verified recording evidence and an immutable final revision on the source's prior record.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "association-final-revision.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        previous = LibraryRecord(id='record-previous', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-final-revision',
            source_path='/incoming/final-revision.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=previous,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='musicbrainzmatch', snapshot_sha256='b' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='recording',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.98,"tags":'
                        '{"MUSICBRAINZ_ALBUMID":"release-id",'
                        '"MUSICBRAINZ_RECORDINGID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
        )
        session.add_all((previous, source))
        session.flush()
        original = append_metadata_revision(
            session, previous.id, source.id, 'final', {'TITLE': 'Fixture'}, 'worker', now
        )
        session.commit()

        # When: automatic association moves the source to the verified recording aggregate.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(
                source.id, recording_mbid, 0.98, 0.9, '{"provider":"worker"}', now, release_mbid
            )
        )
        session.commit()

        # Then: prior provenance remains immutable and the target gets a new final revision for the same source.
        assert result is not None
        retained = session.get(type(original), original.id)
        target = session.get(LibraryRecord, result.library_record_id)
        assert retained is not None
        assert target is not None
        target_revisions = [
            revision
            for revision in target.metadata_revisions
            if revision.source_id == source.id and revision.layer == 'final'
        ]
        assert retained.library_record_id == previous.id
        assert [(revision.tags_json, revision.actor) for revision in target_revisions] == [
            ('{"TITLE": "Fixture"}', 'reassociation')
        ]


def test_automatic_association_prefers_target_provider_metadata_over_source_fallback(tmp_path: Path) -> None:
    # Given: the target recording already has provider metadata from another source.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "association-provider-final.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    release_mbid = 'release-id'
    with Session(engine) as session:
        previous = LibraryRecord(id='record-previous-provider', created_at=now, updated_at=now)
        target = LibraryRecord(
            id='record-target-provider',
            musicbrainz_recording_id=recording_mbid,
            musicbrainz_release_id=release_mbid,
            match_state='matched',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source-provider-fallback',
            source_path='/incoming/provider-fallback.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=previous,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='musicbrainzmatch', snapshot_sha256='b' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='recording',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.98,"tags":'
                        '{"MUSICBRAINZ_ALBUMID":"release-id",'
                        '"MUSICBRAINZ_RECORDINGID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
        )
        session.add_all((previous, target, source))
        session.flush()
        _ = append_metadata_revision(session, previous.id, source.id, 'final', {'TITLE': 'Source'}, 'worker', now)
        _ = append_metadata_revision(
            session, target.id, 'other-source', 'final', {'TITLE': 'Provider'}, 'provider', now
        )
        session.commit()

        # When: automatic association moves the source to the provider-backed target record.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(
                source.id, recording_mbid, 0.98, 0.9, '{"provider":"worker"}', now, release_mbid
            )
        )
        session.commit()

        # Then: the reassociated source receives the target's analyzed final metadata.
        assert result is not None
        revisions = [
            revision
            for revision in target.metadata_revisions
            if revision.source_id == source.id and revision.layer == 'final'
        ]
        assert [(revision.tags_json, revision.actor) for revision in revisions] == [
            ('{"TITLE": "Provider"}', 'reassociation')
        ]


def test_automatic_association_when_recording_is_not_durably_confirmed_requires_review(tmp_path: Path) -> None:
    # Given: a high-score request with no persisted MusicBrainz recording confirmation.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "unverified-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    recording_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    with Session(engine) as session:
        record = LibraryRecord(id='record-unverified', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-unverified',
            source_path='/incoming/unverified.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        session.commit()

        # When: an arbitrary caller supplies a recording MBID and sufficient score.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(source.id, recording_mbid, 0.99, 0.9, '{"provider":"caller"}', now)
        )
        session.commit()

        # Then: no aggregate identity is created and the source is reviewable.
        persisted = session.get(SourceRecord, source.id)
        assert result is None
        assert persisted is not None
        assert persisted.library_record_id == record.id
        assert persisted.review_decisions[-1].state == 'association_review_required'


def test_automatic_association_when_manual_override_is_active_does_not_move_source(tmp_path: Path) -> None:
    # Given: a source has durable alternate recording evidence but an active manual override.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "override-blocks-auto.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    automatic_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    override_mbid = '11111111-1111-4111-8111-111111111111'
    with Session(engine) as session:
        record = LibraryRecord(id='record-override', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-override',
            source_path='/incoming/override.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz', outcome='musicbrainzmatch', snapshot_sha256='b' * 64, snapshot='{}'
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='release',
                    evidence=(
                        '{"provider":"musicbrainz","score":0.99,"tags":'
                        '{"MUSICBRAINZ_TRACKID":"f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a"}}'
                    ),
                )
            ],
            association_override=SourceAssociationOverrideRecord(
                recording_mbid=override_mbid,
                actor='reviewer@example.test',
                rationale='Manual correction.',
                created_at=now,
            ),
        )
        session.add_all((record, source))
        session.commit()

        # When: a later automatic match proposes a different verified recording.
        result = RecordingAssociationService(session).associate_automatic(
            AutomaticAssociationRequest(source.id, automatic_mbid, 0.99, 0.9, '{"provider":"worker"}', now)
        )
        session.commit()

        # Then: the source and operator override remain unchanged and review is retained.
        persisted = session.get(SourceRecord, source.id)
        assert result is None
        assert persisted is not None
        assert persisted.library_record_id == record.id
        assert persisted.association_override is not None
        assert persisted.association_override.recording_mbid == override_mbid


def test_manual_association_when_verified_mbid_is_absent_from_candidates_moves_source(tmp_path: Path) -> None:
    # Given: a source whose retained provider evidence names a different recording.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "conflicting-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    requested_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'
    with Session(engine) as session:
        record = LibraryRecord(id='record-conflict', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-conflict',
            source_path='/incoming/conflict.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
            candidates=[
                CandidateRecord(
                    candidate_key='different-recording',
                    evidence='{"provider":"acoustid","recording_mbid":"11111111-1111-4111-8111-111111111111"}',
                )
            ],
        )
        session.add_all((record, source))
        session.commit()

        # When: a reviewer supplies a verified MusicBrainz recording absent from the candidates.
        service = RecordingAssociationService(session, MusicBrainzFixtureProvider(FIXTURES_DIRECTORY / 'musicbrainz'))
        result = service.associate_manual(ManualAssociationRequest(source.id, requested_mbid, now))
        session.commit()

        # Then: the source stays reviewable until a release is selected with the recording.
        persisted = session.get(SourceRecord, source.id)
        target = session.get(LibraryRecord, result.library_record_id)
        assert persisted is not None
        assert persisted.library_record_id == result.library_record_id
        assert result.moved_from_record_id == record.id
        assert target is not None
        assert target.musicbrainz_recording_id is None
        assert target.musicbrainz_release_id is None
        assert target.processing_state == 'needs_review'


def test_manual_association_accepts_release_ambiguity_for_one_verified_recording(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "ambiguous-release-association.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 12, tzinfo=UTC)
    requested_mbid = 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'

    class Provider:
        def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzResult:
            del request, now
            provenance = FixtureProvenance(Path('ambiguous-recording.json'), 'a' * 64)
            return Ambiguous(
                provenance,
                (
                    ReleaseCandidate('release-one', 'First release', 'Artist', recording_mbids=(requested_mbid,)),
                    ReleaseCandidate('release-two', 'Second release', 'Artist', recording_mbids=(requested_mbid,)),
                ),
            )

    with Session(engine) as session:
        record = LibraryRecord(id='record-ambiguous-release', created_at=now, updated_at=now)
        source = SourceRecord(
            id='source-ambiguous-release',
            source_path='/incoming/ambiguous.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            library_record=record,
        )
        session.add_all((record, source))
        session.commit()

        result = RecordingAssociationService(session, Provider()).associate_manual(
            ManualAssociationRequest(source.id, requested_mbid, now)
        )
        session.commit()

        target = session.get(LibraryRecord, result.library_record_id)
        persisted = session.get(SourceRecord, source.id)
        assert target is not None
        assert persisted is not None
        assert target.musicbrainz_recording_id is None
        assert target.musicbrainz_release_id is None
        assert target.processing_state == 'needs_review'
        assert persisted.association_override is not None
        assert persisted.association_override.rationale == 'MusicBrainz recording selected manually'
        assert persisted.recording_assignments == []
