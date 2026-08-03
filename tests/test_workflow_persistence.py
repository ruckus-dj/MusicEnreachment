from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from alembic.config import Config
from sqlalchemy import create_engine, inspect
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from alembic import command
from music_ingest.persistence.models import (
    AuditRecord,
    Base,
    JobAttemptRecord,
    JobRecord,
    PublicationStateRecord,
    ReleaseFileRecord,
    ReleaseGroupRecord,
    ReleaseRecord,
    TagLayerRecord,
    TombstoneRecord,
    TrackRecord,
    WebhookReceiptRecord,
)


def test_workflow_schema_when_migrated_exposes_replay_safe_release_lifecycle_tables(tmp_path: Path) -> None:
    # Given: an empty database and the production Alembic lineage.
    database_path = tmp_path / 'workflow.db'
    config = Config()
    config.set_main_option('script_location', str(Path(__file__).parents[1] / 'alembic'))
    config.set_main_option('sqlalchemy.url', f'sqlite+pysqlite:///{database_path}')

    # When: the lineage upgrades to head.
    command.upgrade(config, 'head')

    # Then: the durable workflow tables are present without persisting webhook headers or credentials.
    tables = set(inspect(create_engine(f'sqlite+pysqlite:///{database_path}')).get_table_names())
    assert {
        'webhook_receipts',
        'release_groups',
        'releases',
        'tracks',
        'release_files',
        'tag_layers',
        'jobs',
        'job_attempts',
        'publication_states',
        'tombstones',
        'audit_records',
    } <= tables
    receipt_columns = {
        column['name']
        for column in inspect(create_engine(f'sqlite+pysqlite:///{database_path}')).get_columns('webhook_receipts')
    }
    assert {'authorization', 'api_key', 'secret', 'headers'}.isdisjoint(receipt_columns)


def test_workflow_records_when_duplicate_fingerprint_or_tag_revision_replayed_rejects_second_write(
    tmp_path: Path,
) -> None:
    # Given: a release file with the first raw receipt and original tag revision.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "workflow.db"}')
    Base.metadata.create_all(engine)
    received_at = datetime(2026, 7, 29, tzinfo=UTC)
    with Session(engine) as session:
        group = ReleaseGroupRecord(id='group-1', title='Fixture Group')
        release = ReleaseRecord(id='release-1', release_group=group, title='Fixture Release')
        track = TrackRecord(id='track-1', release=release, position=1, title='Fixture Track')
        release_file = ReleaseFileRecord(
            id='file-1', track=track, relative_path='Fixture/01.flac', content_sha256='a' * 64
        )
        session.add_all(
            (
                WebhookReceiptRecord(
                    event_fingerprint='b' * 64,
                    provider_name='lidarr',
                    payload_json='{"event":"Download"}',
                    received_at=received_at,
                ),
                release_file,
                TagLayerRecord(
                    release_file=release_file,
                    layer='original',
                    revision=1,
                    tags_json='{"TITLE":"Observed"}',
                    recorded_at=received_at,
                ),
            )
        )
        session.commit()

    # When: the same event fingerprint or layer revision is persisted again.
    with Session(engine) as session:
        session.add(
            WebhookReceiptRecord(
                event_fingerprint='b' * 64,
                provider_name='lidarr',
                payload_json='{"event":"Download"}',
                received_at=received_at,
            )
        )
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()
        session.add(
            TagLayerRecord(
                release_file_id='file-1',
                layer='original',
                revision=1,
                tags_json='{"TITLE":"Observed"}',
                recorded_at=received_at,
            )
        )
        with pytest.raises(IntegrityError):
            session.commit()

    # Then: uniqueness makes replay and stale revisions non-successful writes.


def test_workflow_records_when_lifecycle_is_persisted_links_jobs_publication_tombstone_and_audit(
    tmp_path: Path,
) -> None:
    # Given: a persisted release file in an analyzed workflow state.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "workflow.db"}')
    Base.metadata.create_all(engine)
    recorded_at = datetime(2026, 7, 29, tzinfo=UTC)
    with Session(engine) as session:
        group = ReleaseGroupRecord(id='group-1', title='Fixture Group')
        release = ReleaseRecord(id='release-1', release_group=group, title='Fixture Release')
        track = TrackRecord(id='track-1', release=release, position=1, title='Fixture Track')
        release_file = ReleaseFileRecord(
            id='file-1', track=track, relative_path='Fixture/01.flac', content_sha256='a' * 64
        )
        job = JobRecord(id='job-1', kind='analyze', state='completed', created_at=recorded_at)
        session.add_all(
            (
                release_file,
                TagLayerRecord(
                    release_file=release_file,
                    layer='analyzed',
                    revision=1,
                    tags_json='{"TITLE":"Analyzed"}',
                    recorded_at=recorded_at,
                ),
                TagLayerRecord(
                    release_file=release_file,
                    layer='final',
                    revision=1,
                    tags_json='{"TITLE":"Final"}',
                    recorded_at=recorded_at,
                ),
                JobAttemptRecord(
                    job=job,
                    attempt_number=1,
                    state='succeeded',
                    started_at=recorded_at,
                    finished_at=recorded_at,
                ),
                PublicationStateRecord(release=release, state='published', updated_at=recorded_at),
                TombstoneRecord(release_file=release_file, reason='superseded', recorded_at=recorded_at),
                AuditRecord(
                    release=release,
                    action='published',
                    actor='worker',
                    details_json='{"release":"release-1"}',
                    recorded_at=recorded_at,
                ),
            )
        )
        session.commit()

    # When: the durable graph is loaded through its release root.
    with Session(engine) as session:
        persisted = session.get(ReleaseRecord, 'release-1')

        # Then: publication, immutable tag layers, attempts, tombstone, and audit are all retained.
        assert persisted is not None
        assert persisted.publication is not None
        assert persisted.publication.state == 'published'
        assert [layer.layer for layer in persisted.tracks[0].files[0].tag_layers] == ['analyzed', 'final']
        assert persisted.tracks[0].files[0].tombstone is not None
        assert persisted.audits[0].action == 'published'
        assert session.get(JobRecord, 'job-1') is not None
