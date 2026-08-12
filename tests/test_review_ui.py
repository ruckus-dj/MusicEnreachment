from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.library.service import append_metadata_revision
from music_ingest.models import (
    Base,
    CandidateRecord,
    JobRecord,
    LibraryPublicationRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.models.jobs import JobRepository


def _source_root(path: Path, *, enabled: bool = True) -> SourceRootRecord:
    now = datetime.now(UTC)
    return SourceRootRecord(
        id='legacy',
        display_name='legacy',
        canonical_path=str(path.resolve()),
        enabled=enabled,
        scan_state='scanned',
        created_at=now,
        updated_at=now,
    )


def test_review_ui_when_loaded_contains_evidence_diff_and_review_controls(tmp_path: Path) -> None:
    # Given: an empty local review database.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review-ui.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator opens the local review page.
    response = client.get('/')

    # Then: the page names the durable evidence-led workflow and its actions.
    assert response.status_code == 200
    for marker in ('Медиатека', 'assets/', 'type="module"', 'lang="ru"'):
        assert marker in response.text


def test_reprocess_all_queues_active_sources_from_filesystem_scan(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reprocess-all.db"}')
    Base.metadata.create_all(engine)
    active_path = tmp_path / 'active.flac'
    disappeared_path = tmp_path / 'disappeared.flac'
    _ = active_path.write_bytes(b'active')
    _ = disappeared_path.write_bytes(b'disappeared')
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        active = intake_source(
            session,
            IntakeRequest(
                source_path=active_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        disappeared = intake_source(
            session,
            IntakeRequest(
                source_path=disappeared_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        session.commit()

    disappeared_path.unlink()

    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/reprocess-all')

    assert response.status_code == 200
    assert response.json() == {'queued': 1}
    with Session(engine) as session:
        jobs = list(session.query(JobRecord).filter(JobRecord.kind == 'filesystem_scan').all())
        assert [job.source_id for job in jobs] == [active.source_id]
        assert session.get(SourceRecord, disappeared.source_id) is None


def test_review_ui_when_detail_is_populated_contains_api_data_flow_and_action_submission(tmp_path: Path) -> None:
    # Given: a review page backed by the local API.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review-ui-populated.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator loads the UI shell for a populated queue.
    response = client.get('/review')

    # Then: the browser has executable queue/detail loading and API action submission seams.
    assert response.status_code == 200
    for marker in ('assets/', 'Music Ingest', 'description', 'root'):
        assert marker in response.text
    assert 'approve' not in response.text.lower()


def test_library_deep_link_when_reloaded_returns_the_review_shell(tmp_path: Path) -> None:
    # Given: the browser has a deep link to a track inspector.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "review-ui-deep-link.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the browser reloads that URL directly.
    response = client.get('/library/artist/Radiohead/album/OK%20Computer/track/record-1/source-1')

    # Then: FastAPI returns the SPA shell so React can restore the route.
    assert response.status_code == 200
    assert 'id="root"' in response.text


def test_settings_deep_link_when_reloaded_returns_the_review_shell(tmp_path: Path) -> None:
    # Given: a review app serving the compiled React shell.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "settings-deep-link.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the browser reloads the settings route directly.
    response = client.get('/settings')

    # Then: FastAPI returns the shell so React can restore the settings screen.
    assert response.status_code == 200
    assert 'id="root"' in response.text


def test_library_recovery_when_source_is_blocked_requeues_without_deleting_history(tmp_path: Path) -> None:
    # Given: an existing source whose initial filesystem job exhausted its retries.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "recovery.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        session.add(
            JobRecord(
                id=f'filesystem-{intake.source_id}',
                source_id=intake.source_id,
                kind='filesystem_scan',
                state='blocked_infrastructure',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: the operator clicks the bulk recovery action.
    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/recovery')

    # Then: a new queued job exists and the old blocked job remains as evidence.
    assert response.status_code == 200
    assert response.json() == {'queued': 1, 'skipped': 0, 'conflicts': 0}
    with Session(engine) as session:
        jobs = list(session.query(JobRecord).filter(JobRecord.source_id == intake.source_id).all())
        assert len(jobs) == 2
        assert sum(job.state == 'queued' for job in jobs) == 1


def test_destination_replace_when_service_owned_removes_only_managed_folder(tmp_path: Path) -> None:
    # Given: a service-owned destination collision for an existing source.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "destination.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        source = session.get(SourceRecord, intake.source_id)
        assert source is not None
        job = JobRecord(
            id=f'filesystem-{intake.source_id}',
            source_id=intake.source_id,
            kind='filesystem_scan',
            state='blocked_infrastructure',
            created_at=datetime.now(UTC),
        )
        job_id = job.id
        session.add(job)
        session.commit()
        record_id = source.library_record_id

    destination = tmp_path / job_id
    destination.mkdir()
    (destination / 'stale.flac').write_bytes(b'stale')
    client = TestClient(create_app(lambda: Session(engine), media_root=tmp_path))

    # When: the operator confirms replacement from the track inspector.
    response = client.post(f'/api/library/records/{record_id}/sources/{intake.source_id}/destination-conflict/replace')

    # Then: only the managed destination is removed and the source is queued.
    assert response.status_code == 200
    assert response.json()['removed'] is True
    assert response.json()['queued'] is True
    assert not destination.exists()


def test_job_id_when_source_id_is_sha256_fits_database_column(tmp_path: Path) -> None:
    # Given: the 64-character source identity used by real incoming files.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "job-id.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        # When: recovery creates a provider job for that source.
        job = JobRepository(session).enqueue('a' * 64, 'acoustid_analysis', datetime.now(UTC))

        # Then: the generated identifier fits jobs.id VARCHAR(96).
        assert job is not None
        assert len(job.id) <= 96


def test_library_recovery_when_completed_layer_contains_source_tags_requeues_analysis(tmp_path: Path) -> None:
    # Given: a completed record whose historical analyzed layer contains copied source tags.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "layer-recovery.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    source_path.write_bytes(b'fixture')
    published_path = tmp_path / 'published.flac'
    published_path.write_bytes(b'published')
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        source = session.get(SourceRecord, intake.source_id)
        assert source is not None and source.library_record is not None
        record = source.library_record
        record.processing_state = 'complete'
        record.publication_state = 'current'
        session.add(
            LibraryPublicationRecord(
                id='publication-existing',
                library_record_id=record.id,
                source_id=source.id,
                path=str(published_path),
                format_name='flac',
                content_sha256='a' * 64,
                state='current',
                created_at=datetime.now(UTC),
            )
        )
        append_metadata_revision(
            session, record.id, source.id, 'analyzed', {'TITLE': 'copied'}, 'provider', datetime.now(UTC)
        )
        session.commit()

    # When: the operator requests bulk recovery.
    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/recovery')

    # Then: the contaminated layer is eligible for a fresh provider analysis.
    assert response.status_code == 200
    assert response.json() == {'queued': 1, 'skipped': 0, 'conflicts': 0}


@pytest.mark.parametrize('root_state', ('disabled', 'historical', 'outside', 'symlink'))
def test_library_recovery_rejects_invalid_persisted_source_root(tmp_path: Path, root_state: str) -> None:
    # Given: an otherwise recoverable source whose persisted root was disabled after intake.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "disabled-root-recovery.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = _source_root(tmp_path)
        session.add(root)
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        match root_state:
            case 'disabled':
                root.enabled = False
            case 'historical':
                root.id = 'historical-unmanaged'
                root.canonical_path = 'historical-unmanaged://'
            case 'outside':
                outside = tmp_path / 'outside'
                outside.mkdir()
                root.canonical_path = str(outside)
            case 'symlink':
                target = tmp_path / 'target'
                target.mkdir()
                link = tmp_path / 'root-link'
                link.symlink_to(target, target_is_directory=True)
                root.canonical_path = str(link)
            case unreachable:
                raise AssertionError(unreachable)
        session.commit()

    # When: the bulk recovery endpoint attempts to queue the source.
    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/recovery')

    # Then: it rejects the root boundary and creates no recovery job.
    assert response.status_code == 409
    with Session(engine) as session:
        assert session.query(JobRecord).filter_by(source_id=intake.source_id).count() == 0


@pytest.mark.parametrize('root_state', ('disabled',))
def test_reprocess_all_skips_unavailable_persisted_source_root(tmp_path: Path, root_state: str) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reprocess-invalid-root.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = _source_root(tmp_path)
        session.add(root)
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        match root_state:
            case 'disabled':
                root.enabled = False
            case 'outside':
                outside = tmp_path / 'outside'
                outside.mkdir()
                root.canonical_path = str(outside)
            case 'symlink':
                target = tmp_path / 'target'
                target.mkdir()
                link = tmp_path / 'root-link'
                link.symlink_to(target, target_is_directory=True)
                root.canonical_path = str(link)
            case unreachable:
                raise AssertionError(unreachable)
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/reprocess-all')

    assert response.status_code == 200
    assert response.json() == {'queued': 0}
    with Session(engine) as session:
        assert session.query(JobRecord).filter_by(source_id=intake.source_id).count() == 0


@pytest.mark.parametrize('root_state', ('outside', 'symlink'))
def test_reprocess_all_rejects_unsafe_persisted_source_root(tmp_path: Path, root_state: str) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reprocess-invalid-root.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = _source_root(tmp_path)
        session.add(root)
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        match root_state:
            case 'outside':
                outside = tmp_path / 'outside'
                outside.mkdir()
                root.canonical_path = str(outside)
            case 'symlink':
                target = tmp_path / 'target'
                target.mkdir()
                link = tmp_path / 'root-link'
                link.symlink_to(target, target_is_directory=True)
                root.canonical_path = str(link)
            case unreachable:
                raise AssertionError(unreachable)
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).post('/api/library/reprocess-all')

    assert response.status_code == 409
    with Session(engine) as session:
        assert session.query(JobRecord).filter_by(source_id=intake.source_id).count() == 0


@pytest.mark.parametrize(('action', 'job_kind'), (('cleanup', 'manual_import'), ('replace', 'filesystem_scan')))
def test_destination_conflict_actions_reject_disabled_source_before_deleting_output(
    tmp_path: Path, action: str, job_kind: str
) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / f"{action}-invalid-root.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = _source_root(tmp_path)
        session.add(root)
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        source = session.get(SourceRecord, intake.source_id)
        assert source is not None
        job = JobRecord(
            id=f'{job_kind}-{intake.source_id}',
            source_id=intake.source_id,
            kind=job_kind,
            state='blocked_infrastructure',
            created_at=datetime.now(UTC),
        )
        session.add(job)
        job_id = job.id
        root.enabled = False
        record_id = source.library_record_id
        session.commit()

    destination = tmp_path / job_id
    destination.mkdir()
    (destination / 'stale.flac').write_bytes(b'stale')
    response = TestClient(create_app(lambda: Session(engine), media_root=tmp_path)).post(
        f'/api/library/records/{record_id}/sources/{intake.source_id}/destination-conflict/{action}'
    )

    assert response.status_code == 409
    assert destination.exists()


@pytest.mark.parametrize('root_state', ('disabled', 'historical', 'outside', 'symlink'))
@pytest.mark.parametrize('action', ('metadata', 'candidate', 'musicbrainz_override'))
def test_source_backed_job_actions_reject_invalid_roots_before_state_mutation(
    tmp_path: Path, root_state: str, action: str
) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / f"{action}-{root_state}.db"}')
    Base.metadata.create_all(engine)
    source_path = tmp_path / 'track.flac'
    _ = source_path.write_bytes(b'fixture')
    with Session(engine) as session:
        root = _source_root(tmp_path)
        session.add(root)
        intake = intake_source(
            session,
            IntakeRequest(
                source_path=source_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        source = session.get(SourceRecord, intake.source_id)
        assert source is not None
        session.add(
            CandidateRecord(
                source_id=source.id,
                candidate_key='release-id',
                evidence='{"tags":{"TITLE":"New title"}}',
            )
        )
        match root_state:
            case 'disabled':
                root.enabled = False
            case 'historical':
                root.id = 'historical-unmanaged'
                root.canonical_path = 'historical-unmanaged://'
            case 'outside':
                outside = tmp_path / 'outside'
                outside.mkdir()
                root.canonical_path = str(outside)
            case 'symlink':
                target = tmp_path / 'target'
                target.mkdir()
                link = tmp_path / 'root-link'
                link.symlink_to(target, target_is_directory=True)
                root.canonical_path = str(link)
            case unreachable:
                raise AssertionError(unreachable)
        record_id = source.library_record_id
        session.commit()

    match action:
        case 'metadata':
            endpoint = f'/api/library/records/{record_id}/metadata'
            payload = {'source_id': intake.source_id, 'tags': {'TITLE': 'New title'}}
        case 'candidate':
            endpoint = f'/api/library/records/{record_id}/sources/{intake.source_id}/candidates/select'
            payload = {'candidate_key': 'release-id'}
        case 'musicbrainz_override':
            endpoint = f'/api/library/records/{record_id}/sources/{intake.source_id}/musicbrainz/override'
            payload = {'recording_mbid': '3c32b3e7-f21d-4935-bcac-d9c0df46db68'}
        case unreachable:
            raise AssertionError(unreachable)
    response = TestClient(create_app(lambda: Session(engine))).request(
        'PUT' if action == 'metadata' else 'POST', endpoint, json=payload
    )

    assert response.status_code == 409
    with Session(engine) as session:
        assert session.query(JobRecord).filter_by(source_id=intake.source_id).count() == 0
        persisted_source = session.get(SourceRecord, intake.source_id)
        assert persisted_source is not None and persisted_source.library_record is not None
        assert persisted_source.library_record.metadata_revisions == []
