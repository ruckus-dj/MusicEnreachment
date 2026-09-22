from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    CandidateRecord,
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
        service = RecordingAssociationService(session, MusicBrainzFixtureProvider(Path('tests/fixtures/musicbrainz')))
        result = service.associate_manual(ManualAssociationRequest(source.id, requested_mbid, now))
        session.commit()

        # Then: the source is separated into the requested recording aggregate.
        persisted = session.get(SourceRecord, source.id)
        target = session.get(LibraryRecord, result.library_record_id)
        assert persisted is not None
        assert persisted.library_record_id == result.library_record_id
        assert result.moved_from_record_id == record.id
        assert target is not None
        assert target.musicbrainz_recording_id == requested_mbid
