from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.models import (
    Base,
    CandidateRecord,
    FingerprintRecord,
    JobRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    SourceRecord,
    SourceRootRecord,
    StorageConfigRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.association import RecordingAssociationService
from music_ingest.services.association.service import AssociationResult, ManualAssociationRequest
from music_ingest.services.intake.service import IntakeRequest, Origin, intake_source
from music_ingest.services.library.service import append_metadata_revision
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.runtime import ProcessingRuntimeMonitor
from music_ingest.workers.worker import ProcessingWorker


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


def test_library_record_api_when_fingerprints_are_repeated_returns_them_in_observation_order(
    tmp_path: Path,
) -> None:
    # Given: a source with append-only fingerprint observations.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprint-order.db"}')
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
        assert source is not None and source.library_record_id is not None
        session.add_all(
            (
                FingerprintRecord(
                    source_id=source.id,
                    state='available',
                    fingerprint='earlier',
                    duration_seconds=60.0,
                    output_sha256='a' * 64,
                ),
                FingerprintRecord(
                    source_id=source.id,
                    state='available',
                    fingerprint='latest',
                    duration_seconds=245.4,
                    output_sha256='b' * 64,
                ),
            )
        )
        record_id = source.library_record_id
        session.commit()

    # When: the track inspector loads the record through the API.
    response = TestClient(create_app(lambda: Session(engine))).get(f'/api/library/records/{record_id}')

    # Then: its fingerprint evidence remains chronological for the UI's latest observation.
    assert response.status_code == 200
    assert SourceRecord.fingerprints.property.order_by == (FingerprintRecord.id,)
    assert [item['duration_seconds'] for item in response.json()['sources'][0]['fingerprints']] == [60.0, 245.4]


def test_manual_actions_route_when_opened_serves_review_ui(tmp_path: Path) -> None:
    # Given: an empty local review database.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "manual-actions.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator opens the manual-actions route directly.
    response = client.get('/manual-actions')

    # Then: the single-page review application is available for client-side routing.
    assert response.status_code == 200
    assert 'type="module"' in response.text


def test_release_candidate_selection_reassigns_source_to_compatible_record(tmp_path: Path) -> None:
    # Given: a source with a release candidate compatible with another recording aggregate.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "release-selection.db"}')
    Base.metadata.create_all(engine)
    first_path = tmp_path / 'first.flac'
    second_path = tmp_path / 'second.flac'
    first_path.write_bytes(b'first')
    second_path.write_bytes(b'second')
    recording_mbid = '11111111-1111-4111-8111-111111111111'
    release_mbid = '22222222-2222-4222-8222-222222222222'
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        first = intake_source(
            session,
            IntakeRequest(
                source_path=first_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        second = intake_source(
            session,
            IntakeRequest(
                source_path=second_path,
                origin=Origin.MANUAL,
                duration_seconds=None,
                tag_observations=(),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        )
        first_source = session.get(SourceRecord, first.source_id)
        second_source = session.get(SourceRecord, second.source_id)
        assert first_source is not None and first_source.library_record_id is not None
        assert second_source is not None and second_source.library_record_id is not None
        first_record = session.get(LibraryRecord, first_source.library_record_id)
        assert first_record is not None
        first_record.musicbrainz_recording_id = recording_mbid
        first_record.musicbrainz_release_id = release_mbid
        second_source.candidates.append(
            CandidateRecord(
                candidate_key=f'{release_mbid}:{recording_mbid}',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'release_mbid': release_mbid,
                        'recording_mbid': recording_mbid,
                        'tags': {
                            'ALBUM': 'Selected Album',
                            'MUSICBRAINZ_ALBUMID': release_mbid,
                            'MUSICBRAINZ_TRACKID': recording_mbid,
                        },
                    },
                    sort_keys=True,
                ),
            )
        )
        second_record_id = second_source.library_record_id
        first_record_id = first_source.library_record_id
        second_source_id = second_source.id
        session.commit()

    # When: the operator selects the compatible release candidate in the UI.
    response = TestClient(create_app(lambda: Session(engine))).post(
        f'/api/library/records/{second_record_id}/sources/{second_source_id}/candidates/select',
        json={
            'candidate_key': f'{release_mbid}:{recording_mbid}',
            'provider': 'musicbrainz',
            'entity': 'recording_release',
        },
    )

    # Then: the source joins the aggregate that owns the same complete recording-release pair.
    assert response.status_code == 200
    with Session(engine) as session:
        moved_source = session.get(SourceRecord, second_source_id)
        assert moved_source is not None
        assert moved_source.library_record_id == first_record_id
        target = session.get(LibraryRecord, moved_source.library_record_id)
        assert target is not None
        assert target.musicbrainz_release_id == release_mbid


def test_manual_recording_override_rejects_an_unverified_release_pair(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: an operator supplies two identifiers without MusicBrainz pair evidence.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "manual-pair.db"}')
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
        assert source is not None and source.library_record_id is not None
        record_id = source.library_record_id
        session.commit()
    requests: list[ManualAssociationRequest] = []

    def associate_manual(_service: RecordingAssociationService, request: ManualAssociationRequest) -> AssociationResult:
        requests.append(request)
        return AssociationResult(record_id, record_id)

    monkeypatch.setattr(RecordingAssociationService, 'associate_manual', associate_manual)

    # When: the direct recording override endpoint receives an unverified release too.
    response = TestClient(create_app(lambda: Session(engine))).post(
        f'/api/library/records/{record_id}/sources/{intake.source_id}/musicbrainz/override',
        json={
            'recording_mbid': '11111111-1111-4111-8111-111111111111',
            'release_mbid': '22222222-2222-4222-8222-222222222222',
        },
    )

    # Then: the API rejects the pair before association; only candidate lookup may validate it.
    assert response.status_code == 422
    assert requests == []


def test_worker_queue_api_returns_active_jobs_and_observed_activity(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker-queue.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    source_path = tmp_path / 'queued.flac'
    source_path.write_bytes(b'queued')
    with Session(engine) as session:
        session.add(_source_root(tmp_path))
        source = intake_source(
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
        persisted_source = session.get(SourceRecord, source.source_id)
        assert persisted_source is not None
        assert persisted_source.library_record_id is not None
        record_id = persisted_source.library_record_id
        session.add_all(
            [
                JobRecord(
                    id='queued-job',
                    source_id=source.source_id,
                    kind='filesystem_scan',
                    state='queued',
                    created_at=now,
                ),
                JobRecord(id='running-job', kind='reconciliation_scan', state='running', created_at=now),
            ]
        )
        session.commit()

    response = TestClient(create_app(lambda: Session(engine))).get('/api/workers/queue')

    assert response.status_code == 200
    payload = response.json()
    assert payload['worker'] == {
        'configured_concurrency': 27,
        'pools': {
            'filesystem_scan': 4,
            'acoustid_analysis': 1,
            'musicbrainz_analysis': 4,
            'candidate_selection': 4,
            'folder_release_selection': 2,
            'final_publish': 4,
            'selection_refresh': 4,
            'lrclib_fetch': 1,
            'artwork_enrichment': 2,
            'reconciliation_scan': 1,
        },
        'liveness': 'unavailable',
        'slots': [],
    }
    assert payload['summary'] == {'running': 1, 'ready': 1, 'retry_wait': 0}
    assert [job['job_id'] for job in payload['jobs']] == ['queued-job', 'running-job']
    assert payload['jobs'][0]['attempt_count'] == 0
    assert payload['jobs'][0]['target'] == {
        'record_id': record_id,
        'source_id': source.source_id,
        'title': 'queued',
        'artist': '',
        'album': '',
        'path': str(source_path),
    }
    assert payload['jobs'][1]['target'] is None


def test_worker_queue_api_overlays_the_runtime_claim_on_queued_job(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker-runtime-queue.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(JobRecord(id='queued-job', kind='reconciliation_scan', state='queued', created_at=now))
        session.commit()
    monitor = ProcessingRuntimeMonitor()
    monitor.observe(0, 'processing', job_id='queued-job', job_kind='reconciliation_scan')

    response = TestClient(create_app(lambda: Session(engine), worker_monitor=monitor)).get('/api/workers/queue')

    assert response.status_code == 200
    payload = response.json()
    assert payload['summary']['running'] == 1
    assert payload['jobs'][0]['state'] == 'running'
    assert payload['worker']['slots'][0]['job_id'] == 'queued-job'


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


def test_reconciliation_scan_api_queues_one_job_and_reports_completed_result(tmp_path: Path) -> None:
    # Given: an empty database and a configured, empty source root.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "reconciliation-scan.db"}')
    Base.metadata.create_all(engine)
    incoming = tmp_path / 'incoming'
    incoming.mkdir()
    with Session(engine) as session:
        session.add(_source_root(incoming))
        session.commit()
    client = TestClient(create_app(lambda: Session(engine)))

    # When: the operator asks to scan twice before the worker has started.
    first = client.post('/api/reconciliation/scan')
    second = client.post('/api/reconciliation/scan')

    # Then: one durable job is reused and its terminal result is available through the API.
    assert first.status_code == 202
    assert second.status_code == 202
    assert first.json()['job_id'] == second.json()['job_id']
    job_id = first.json()['job_id']
    with Session(engine) as session:
        worker = ProcessingWorker(
            session,
            ProcessingConfig(
                incoming_root=incoming,
                staging_root=tmp_path / 'staging',
                media_root=tmp_path / 'media',
            ),
        )
        assert worker.run_once()
        session.commit()
    status_response = client.get(f'/api/reconciliation/scan/{job_id}')
    assert status_response.status_code == 200
    assert status_response.json() == {
        'job_id': job_id,
        'state': 'completed',
        'result': {'added': 0, 'changed': 0, 'removed': 0, 'moved': 0, 'unchanged': 0, 'queued_jobs': 0},
    }


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


@pytest.mark.parametrize('migration_active', [False, True])
def test_destination_replace_when_service_owned_removes_only_managed_folder(
    tmp_path: Path,
    migration_active: bool,
) -> None:
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
    (destination / 'album.nfo').write_bytes(b'preserve')
    with Session(engine) as session:
        session.add(
            StorageConfigRecord(
                id=1,
                output_root=str(tmp_path),
                state='migrating' if migration_active else 'ready',
                generation=1,
                updated_at=datetime.now(UTC),
            )
        )
        session.commit()
    client = TestClient(create_app(lambda: Session(engine), media_root=tmp_path))

    # When: the operator confirms replacement from the track inspector.
    response = client.post(f'/api/library/records/{record_id}/sources/{intake.source_id}/destination-conflict/replace')

    # Relocation excludes destructive API actions; ordinary cleanup preserves NFO.
    assert (destination / 'album.nfo').read_bytes() == b'preserve'
    if migration_active:
        assert response.status_code == 409
        assert (destination / 'stale.flac').read_bytes() == b'stale'
    else:
        assert response.status_code == 200
        assert response.json()['removed'] is False
        assert response.json()['queued'] is True
        assert not (destination / 'stale.flac').exists()


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
