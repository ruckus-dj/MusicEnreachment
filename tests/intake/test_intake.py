from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

import pytest
from alembic.config import Config
from pydantic import ValidationError
from sqlalchemy import create_engine, inspect, select
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from alembic import command
from music_ingest.models import Base, SourceLocationRecord, SourceRecord, SourceRootRecord
from music_ingest.services.intake.service import (
    ArtworkObservation,
    CandidateEvidence,
    IntakeEvidence,
    IntakeRequest,
    Origin,
    ProviderAttempt,
    ReviewDecision,
    SourceTagObservation,
    intake_source,
)
from tests.support.paths import ALEMBIC_DIRECTORY


def intake_request(source: Path, origin: Origin) -> IntakeRequest:
    return IntakeRequest(
        source_path=source,
        origin=origin,
        duration_seconds=241,
        tag_observations=(
            SourceTagObservation(format_name='id3', tag_name='TITLE', value='Live Version'),
            SourceTagObservation(format_name='vorbis', tag_name='TITLE', value='Studio Version'),
        ),
        artwork_observations=(ArtworkObservation(sha256='a' * 64),),
        provider_attempts=(
            ProviderAttempt(
                provider_name='musicbrainz',
                outcome='unavailable',
                snapshot_sha256='b' * 64,
                snapshot='offline fixture',
            ),
        ),
        candidates=(CandidateEvidence(candidate_key='recording-1', evidence='duration match'),),
        review_decisions=(ReviewDecision(state='needs_review', rationale='conflicting source tags'),),
    )


def test_intake_source_when_conflicting_observations_preserves_source_and_database_evidence(tmp_path: Path) -> None:
    # Given: a manual source with mutually conflicting observations.
    source = tmp_path / 'manual.mp3'
    _ = source.write_bytes(b'manual source bytes')
    source_stat = source.stat()
    metadata_fingerprint = sha256(
        f'{source_stat.st_size}:{source_stat.st_mtime_ns}:{source_stat.st_ino}'.encode()
    ).hexdigest()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)

    # When: intake persists the source evidence in the database.
    with Session(engine) as session:
        result = intake_source(session, intake_request(source, Origin.MANUAL))
        session.commit()
        persisted = session.get(SourceRecord, result.source_id)

    # Then: raw observations remain separate, no publication exists yet, and source bytes are untouched.
    assert persisted is not None
    assert persisted.origin == Origin.MANUAL.value
    assert persisted.device is not None
    assert persisted.inode is not None
    assert persisted.size_bytes == len(b'manual source bytes')
    assert persisted.sha256 == metadata_fingerprint
    assert persisted.mtime_ns == source_stat.st_mtime_ns
    assert persisted.duration_seconds == 241
    assert persisted.intake_state == 'needs_review'
    assert [(item.format_name, item.value) for item in persisted.tag_observations] == [
        ('id3', 'Live Version'),
        ('vorbis', 'Studio Version'),
    ]
    assert persisted.library_publications == []
    assert source.read_bytes() == b'manual source bytes'
    assert not (tmp_path / 'provenance').exists()


def test_intake_source_when_distinct_paths_have_identical_bytes_keeps_records_separate(tmp_path: Path) -> None:
    # Given: two immutable observations at distinct paths with exactly the same bytes.
    first = tmp_path / 'first.flac'
    second = tmp_path / 'second.flac'
    _ = first.write_bytes(b'one immutable recording')
    _ = second.write_bytes(b'one immutable recording')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "content-identity.db"}')
    Base.metadata.create_all(engine)

    # When: intake observes each path as an independent source.
    with Session(engine) as session:
        first_result = intake_source(session, intake_request(first, Origin.MANUAL))
        second_result = intake_source(session, intake_request(second, Origin.MANUAL))
        session.commit()
        first_source = session.get(SourceRecord, first_result.source_id)
        second_source = session.get(SourceRecord, second_result.source_id)

    # Then: metadata-only intake retains independent path observations.
    assert first_source is not None
    assert second_source is not None
    assert first_source.id != second_source.id
    assert first_source.source_path != second_source.source_path
    assert first_source.library_record_id != second_source.library_record_id


def test_intake_source_when_repeated_database_identity_does_not_create_files(tmp_path: Path) -> None:
    # Given: a source whose database identity already exists.
    source = tmp_path / 'source.flac'
    _ = source.write_bytes(b'source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)
    request = intake_request(source, Origin.MANUAL)
    with Session(engine) as session:
        first = intake_source(session, request)
        session.commit()

        # When: the same source identity is received again.
        second = intake_source(session, request)
        session.commit()

        # Then: the repeat succeeds without creating a provenance artifact.
        assert second.source_id == first.source_id
        assert not (tmp_path / 'provenance').exists()


def test_intake_source_when_path_identity_changes_transfers_location_to_new_source(tmp_path: Path) -> None:
    # Given: one persisted source path whose immutable identity later changes in place.
    source_path = tmp_path / 'source.flac'
    _ = source_path.write_bytes(b'original source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "changed-source.db"}')
    Base.metadata.create_all(engine)
    request = intake_request(source_path, Origin.MANUAL)
    with Session(engine) as session:
        original = intake_source(session, request)
        session.commit()

    _ = source_path.write_bytes(b'replacement source bytes with a new size')

    # When: intake observes the new immutable version at the occupied path.
    with Session(engine) as session:
        replacement = intake_source(session, request)
        session.commit()

        # Then: the path has exactly one owner and belongs to the replacement version.
        locations = session.scalars(select(SourceLocationRecord)).all()
        original_source = session.get(SourceRecord, original.source_id)
        replacement_source = session.get(SourceRecord, replacement.source_id)
        assert replacement.source_id != original.source_id
        assert [(location.source_id, location.path) for location in locations] == [
            (replacement.source_id, str(source_path.resolve()))
        ]
        assert original_source is not None and original_source.locations == []
        assert replacement_source is not None and len(replacement_source.locations) == 1


def test_intake_source_when_path_changes_to_known_identity_transfers_occupied_location(tmp_path: Path) -> None:
    # Given: two known identities and an original source with a surviving hardlink location.
    changed_path = tmp_path / 'changed.flac'
    surviving_path = tmp_path / 'surviving.flac'
    existing_path = tmp_path / 'existing.flac'
    _ = changed_path.write_bytes(b'original source bytes')
    surviving_path.hardlink_to(changed_path)
    _ = existing_path.write_bytes(b'already known replacement bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "known-identity.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        original = intake_source(session, intake_request(changed_path, Origin.MANUAL))
        _ = intake_source(session, intake_request(surviving_path, Origin.MANUAL))
        known_replacement = intake_source(session, intake_request(existing_path, Origin.MANUAL))
        session.commit()

    changed_path.unlink()
    changed_path.hardlink_to(existing_path)

    # When: intake observes the known replacement identity at the occupied original path.
    with Session(engine) as session:
        replacement = intake_source(session, intake_request(changed_path, Origin.MANUAL))
        session.commit()

        # Then: ownership transfers to the known identity and the original keeps only its surviving hardlink.
        locations = {location.path: location.source_id for location in session.scalars(select(SourceLocationRecord))}
        original_source = session.get(SourceRecord, original.source_id)
        assert replacement.source_id == known_replacement.source_id
        assert locations == {
            str(changed_path.resolve()): known_replacement.source_id,
            str(existing_path.resolve()): known_replacement.source_id,
            str(surviving_path.resolve()): original.source_id,
        }
        assert original_source is not None
        assert original_source.source_path == str(surviving_path.resolve())


@pytest.mark.postgres
def test_intake_source_when_postgres_path_changes_to_known_identity_transfers_location(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # Given: PostgreSQL owns an occupied path, its surviving hardlink, and a known replacement identity.
    monkeypatch.setenv('TESTCONTAINERS_RYUK_DISABLED', 'true')
    changed_path = tmp_path / 'changed.flac'
    surviving_path = tmp_path / 'surviving.flac'
    existing_path = tmp_path / 'existing.flac'
    _ = changed_path.write_bytes(b'original source bytes')
    surviving_path.hardlink_to(changed_path)
    _ = existing_path.write_bytes(b'already known replacement bytes')
    with PostgresContainer('postgres:17') as postgres:
        engine = create_engine(postgres.get_connection_url().replace('postgresql+psycopg2', 'postgresql+psycopg'))
        Base.metadata.create_all(engine)
        now = datetime.now(UTC)
        with Session(engine) as session:
            session.add(
                SourceRootRecord(
                    id='legacy',
                    display_name='legacy',
                    canonical_path=str(tmp_path.resolve()),
                    enabled=True,
                    scan_state='scanned',
                    created_at=now,
                    updated_at=now,
                )
            )
            original = intake_source(session, intake_request(changed_path, Origin.MANUAL))
            _ = intake_source(session, intake_request(surviving_path, Origin.MANUAL))
            known_replacement = intake_source(session, intake_request(existing_path, Origin.MANUAL))
            session.commit()

        changed_path.unlink()
        changed_path.hardlink_to(existing_path)

        # When: intake observes the known replacement identity while locking the occupied location row.
        with Session(engine) as session:
            replacement = intake_source(session, intake_request(changed_path, Origin.MANUAL))
            session.commit()

            # Then: the changed path joins the known identity while the original retains its surviving hardlink.
            locations = {
                location.path: location.source_id for location in session.scalars(select(SourceLocationRecord))
            }
            original_source = session.get(SourceRecord, original.source_id)
            assert replacement.source_id == known_replacement.source_id
            assert locations == {
                str(changed_path.resolve()): known_replacement.source_id,
                str(existing_path.resolve()): known_replacement.source_id,
                str(surviving_path.resolve()): original.source_id,
            }
            assert original_source is not None
            assert original_source.source_path == str(surviving_path.resolve())
        engine.dispose()


@pytest.mark.parametrize(
    ('parser', 'document'),
    (
        (SourceTagObservation, '{"format_name":"","tag_name":"TITLE","value":"value"}'),
        (ArtworkObservation, '{"sha256":"bad-hash"}'),
        (
            ProviderAttempt,
            '{"provider_name":"","outcome":"unavailable","snapshot_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","snapshot":"snapshot"}',
        ),
        (
            ProviderAttempt,
            '{"provider_name":"musicbrainz","outcome":"unavailable","snapshot_sha256":"bad-hash","snapshot":"snapshot"}',
        ),
        (CandidateEvidence, '{"candidate_key":"","evidence":"evidence"}'),
        (ReviewDecision, '{"state":"","rationale":"rationale"}'),
    ),
)
def test_intake_boundary_when_public_evidence_is_invalid_rejects_before_persistence(
    tmp_path: Path, parser: type[IntakeEvidence], document: str
) -> None:
    # Given: a public evidence value that violates the typed boundary.
    source = tmp_path / 'source.mp3'
    _ = source.write_bytes(b'source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)

    # When: construction attempts to parse the malformed public value.
    with pytest.raises(ValidationError):
        _ = parser.model_validate_json(document)

    # Then: no malformed evidence can reach durable source persistence.
    with Session(engine) as session:
        assert session.query(SourceRecord).count() == 0


def test_intake_request_when_duration_is_negative_rejects_before_persistence(tmp_path: Path) -> None:
    # Given: a request with an invalid negative duration.
    source = tmp_path / 'source.mp3'
    _ = source.write_bytes(b'source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)

    # When: the typed request boundary parses it.
    with pytest.raises(ValidationError):
        _ = IntakeRequest(
            source_path=source,
            origin=Origin.MANUAL,
            duration_seconds=-1,
            tag_observations=(),
            artwork_observations=(),
            provider_attempts=(),
            candidates=(),
            review_decisions=(),
        )

    # Then: no source record is persisted.
    with Session(engine) as session:
        assert session.query(SourceRecord).count() == 0


def test_source_tag_observation_when_value_is_null_rejects_boundary_input() -> None:
    # Given: JSON evidence containing a null source tag value.
    document = '{"format_name":"id3","tag_name":"TITLE","value":null}'

    # When: the public evidence parser receives it.
    with pytest.raises(ValidationError):
        _ = SourceTagObservation.model_validate_json(document)

    # Then: null cannot become persisted evidence.


def test_provenance_migration_when_upgraded_and_downgraded_creates_only_its_schema(tmp_path: Path) -> None:
    # Given: a disposable SQLite database and the repository migration directory.
    database_path = tmp_path / 'migration.db'
    config = Config()
    config.set_main_option('script_location', str(ALEMBIC_DIRECTORY))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')

    # When: the complete migration lineage is upgraded then returned to baseline.
    command.upgrade(config, 'head')
    upgraded_tables = inspect(create_engine(f'sqlite+pysqlite:///{database_path}')).get_table_names()
    command.downgrade(config, 'base')
    downgraded_tables = inspect(create_engine(f'sqlite+pysqlite:///{database_path}')).get_table_names()

    # Then: provenance tables exist only at the provenance revision.
    assert 'source_records' in upgraded_tables
    assert 'provider_attempts' in upgraded_tables
    assert 'fingerprint_evidence' in upgraded_tables
    assert 'library_publications' in upgraded_tables
    assert 'fingerprint_evidence' not in downgraded_tables
    assert upgraded_tables != downgraded_tables
