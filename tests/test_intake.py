from __future__ import annotations

from hashlib import sha256
from pathlib import Path

import pytest
from alembic.config import Config
from pydantic import ValidationError
from sqlalchemy import create_engine, inspect
from sqlalchemy.orm import Session

from alembic import command
from music_ingest.intake.service import (
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
from music_ingest.persistence.models import Base, SourceRecord


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
    original_hash = sha256(source.read_bytes()).hexdigest()
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
    assert persisted.sha256 == original_hash
    assert persisted.duration_seconds == 241
    assert persisted.intake_state == 'needs_review'
    assert [(item.format_name, item.value) for item in persisted.tag_observations] == [
        ('id3', 'Live Version'),
        ('vorbis', 'Studio Version'),
    ]
    assert persisted.library_publications == []
    assert source.read_bytes() == b'manual source bytes'
    assert not (tmp_path / 'provenance').exists()


def test_intake_source_when_repeated_lidarr_observation_reuses_source_identity(tmp_path: Path) -> None:
    # Given: a Lidarr source whose path and bytes are observed twice.
    source = tmp_path / 'lidarr.flac'
    _ = source.write_bytes(b'lidarr source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)
    request = intake_request(source, Origin.LIDARR)

    # When: the same source is submitted twice.
    with Session(engine) as session:
        first = intake_source(session, request)
        session.commit()
        second = intake_source(session, request)
        session.commit()
        persisted = session.get(SourceRecord, first.source_id)

    # Then: its durable identity and evidence are idempotent rather than duplicated.
    assert first.source_id == second.source_id
    assert persisted is not None
    assert persisted.origin == Origin.LIDARR.value
    assert len(persisted.tag_observations) == 2
    assert len(persisted.provider_attempts) == 1
    assert first.source_id == second.source_id


def test_intake_source_when_repeated_database_identity_does_not_create_files(tmp_path: Path) -> None:
    # Given: a Lidarr source whose database identity already exists.
    source = tmp_path / 'lidarr.flac'
    _ = source.write_bytes(b'lidarr source bytes')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "intake.db"}')
    Base.metadata.create_all(engine)
    request = intake_request(source, Origin.LIDARR)
    with Session(engine) as session:
        first = intake_source(session, request)
        session.commit()

        # When: the same source identity is received again.
        second = intake_source(session, request)
        session.commit()

        # Then: the repeat succeeds without creating a provenance artifact.
        assert second.source_id == first.source_id
        assert not (tmp_path / 'provenance').exists()


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
    config.set_main_option('script_location', str(Path(__file__).parents[1] / 'alembic'))
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
