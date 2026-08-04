from __future__ import annotations

import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from shutil import which
from subprocess import run
from threading import Barrier
from typing import Final

from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.persistence.models import (
    Base,
    JobRecord,
    ReleaseFileRecord,
    ReleaseGroupRecord,
    ReleaseRecord,
    SourceRecord,
    TombstoneRecord,
    TrackRecord,
    WebhookReceiptRecord,
)
from music_ingest.processing import ProcessingConfig, ProcessingWorker

_FFMPEG: Final[str] = which('ffmpeg') or ''
assert _FFMPEG


def _client(tmp_path: Path) -> tuple[TestClient, Session]:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lidarr.db"}', connect_args={'check_same_thread': False})
    Base.metadata.create_all(engine)
    incoming_root = tmp_path / 'incoming'
    incoming_root.mkdir()
    return TestClient(create_app(lambda: Session(engine), incoming_root=incoming_root)), Session(engine)


def _download(path: Path, *, upgrade: bool = False) -> dict[str, object]:
    payload: dict[str, object] = {
        'eventType': 'Download',
        'instanceName': 'fixture-lidarr',
        'trackFiles': [{'path': str(path)}],
        'isUpgrade': upgrade,
    }
    if upgrade:
        payload['deletedFiles'] = [{'path': str(path.with_name('old.flac'))}]
    return payload


def _flac(path: Path, genre: str = 'Hip Hop; Alternative Rock') -> Path:
    created = run(  # noqa: S603
        [
            _FFMPEG,
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=1',
            '-c:a',
            'flac',
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert created.returncode == 0, created.stderr
    tagged = run(  # noqa: S603
        [  # noqa: S607
            'metaflac',
            '--set-tag=TITLE=Webhook Track',
            '--set-tag=ARTIST=Fixture Artist; Fixture Guest',
            '--set-tag=ALBUM=Webhook Album',
            '--set-tag=ALBUMARTIST=Fixture Artist; Fixture Guest',
            '--set-tag=DATE=2026',
            '--set-tag=TRACKNUMBER=1',
            '--set-tag=TRACKTOTAL=1',
            '--set-tag=DISCNUMBER=1',
            '--set-tag=DISCTOTAL=1',
            f'--set-tag=GENRE={genre}',
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert tagged.returncode == 0, tagged.stderr
    return path


def test_lidarr_download_when_valid_persists_one_receipt_and_queued_job_on_replay(tmp_path: Path) -> None:
    # Given: a mounted incoming file and an otherwise unauthenticated API client.
    client, session = _client(tmp_path)
    source = tmp_path / 'incoming' / 'Artist' / 'Release' / '01.flac'
    source.parent.mkdir(parents=True)
    source.write_bytes(b'raw source bytes')
    payload = _download(source, upgrade=True)

    # When: Lidarr delivers the same Download webhook twice.
    first = client.post('/api/intake/lidarr', json=payload)
    second = client.post('/api/intake/lidarr', json=payload)

    # Then: its raw event is retained once and replay returns the original queued job without processing media.
    assert first.status_code == 202
    assert second.status_code == 202
    assert first.json()['job_id'] == second.json()['job_id']
    assert json.loads(session.scalars(select(WebhookReceiptRecord)).one().payload_json) == payload
    job = session.scalars(select(JobRecord)).one()
    assert job.kind == 'lidarr_download'
    assert job.state == 'queued'
    assert source.read_bytes() == b'raw source bytes'


def test_lidarr_download_when_valid_flac_reaches_the_worker_through_its_durable_source(tmp_path: Path) -> None:
    # Given: a real canonicalizable FLAC delivered through the Download webhook.
    client, session = _client(tmp_path)
    source_path = tmp_path / 'incoming' / 'Artist' / 'Release' / '01.flac'
    source_path.parent.mkdir(parents=True)
    _ = _flac(source_path)
    _ = (source_path.parent / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')

    # When: the webhook persists work and the DB worker claims that work.
    response = client.post('/api/intake/lidarr', json=_download(source_path))
    config = ProcessingConfig(
        incoming_root=tmp_path / 'incoming',
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
    )
    assert ProcessingWorker(session, config).run_once()
    session.commit()

    # Then: the queued webhook job is linked to immutable source provenance and publishes fallback media for review.
    assert response.status_code == 202
    job = session.scalars(select(JobRecord)).one()
    source = session.get(SourceRecord, job.source_id)
    assert job.source_id is not None
    assert job.state == 'completed'
    assert source is not None and source.intake_state == 'needs_review'
    assert [decision.rationale for decision in source.review_decisions] == [
        'provider unavailable; original-tag fallback published'
    ]
    assert next(config.media_root.rglob('*.flac')).is_file()


def test_lidarr_release_import_when_delivered_uses_the_same_source_independent_intake(tmp_path: Path) -> None:
    # Given: a real incoming FLAC and the event name used by manual Lidarr imports.
    client, session = _client(tmp_path)
    source_path = tmp_path / 'incoming' / 'Artist' / 'Release' / '01.flac'
    source_path.parent.mkdir(parents=True)
    _ = _flac(source_path)

    # When: Lidarr delivers a ReleaseImport webhook without download-client metadata.
    payload = _download(source_path)
    payload['eventType'] = 'ReleaseImport'
    response = client.post('/api/intake/lidarr', json=payload)

    # Then: the event is accepted as the same file intake contract and creates one durable job.
    assert response.status_code == 202
    assert session.scalars(select(JobRecord)).one().kind == 'lidarr_releaseimport'


def test_lidarr_download_when_genre_is_unknown_publishes_original_fallback_for_review(tmp_path: Path) -> None:
    # Given: a Download source under a non-default incoming root with an unknown original genre.
    incoming_root = tmp_path / 'runtime-incoming'
    incoming_root.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "unknown-genre.db"}')
    Base.metadata.create_all(engine)
    client = TestClient(create_app(lambda: Session(engine), incoming_root=incoming_root))
    source_path = _flac(incoming_root / 'fixture.flac', genre='Rock')
    _ = (incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')

    # When: the real Download webhook is claimed by the worker.
    response = client.post('/api/intake/lidarr', json=_download(source_path))
    config = ProcessingConfig(
        incoming_root=incoming_root,
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
    )
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: fallback media publishes once and signals review instead of entering retry_wait.
    assert response.status_code == 202
    with Session(engine) as session:
        job = session.scalars(select(JobRecord)).one()
        source = session.get(SourceRecord, job.source_id)
        assert job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        assert source is not None
        assert [decision.rationale for decision in source.review_decisions] == [
            'provider unavailable; original-tag fallback published'
        ]
    assert next(config.media_root.rglob('*.flac')).is_file()


def test_lidarr_download_when_malformed_source_is_claimed_quarantines_its_webhook_job(tmp_path: Path) -> None:
    # Given: an existing incoming path that is not a structurally valid FLAC.
    client, session = _client(tmp_path)
    source_path = tmp_path / 'incoming' / 'Artist' / 'Release' / '01.flac'
    source_path.parent.mkdir(parents=True)
    _ = source_path.write_bytes(b'not a FLAC container')

    # When: the normal webhook and worker boundaries handle the source.
    response = client.post('/api/intake/lidarr', json=_download(source_path))
    config = ProcessingConfig(
        incoming_root=tmp_path / 'incoming',
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
    )
    assert ProcessingWorker(session, config).run_once()
    session.commit()

    # Then: the source remains in place while its durable job and diagnostic are quarantined.
    assert response.status_code == 202
    job = session.scalars(select(JobRecord)).one()
    assert job.state == 'quarantined'
    assert source_path.read_bytes() == b'not a FLAC container'
    assert job.source_id is not None
    assert job.failure_reason == 'structural FLAC inspection failed'
    assert not (tmp_path / 'quarantine').exists()


def test_lidarr_test_when_delivered_returns_no_content_without_a_job(tmp_path: Path) -> None:
    # Given: a webhook test payload and an empty durable workflow.
    client, session = _client(tmp_path)

    # When: Lidarr checks webhook connectivity.
    response = client.post('/api/intake/lidarr', json={'eventType': 'Test', 'instanceName': 'fixture-lidarr'})

    # Then: the endpoint acknowledges it without creating processing work.
    assert response.status_code == 204
    assert session.scalars(select(JobRecord)).all() == []
    assert session.scalars(select(WebhookReceiptRecord)).one().job_id is None


def test_lidarr_rename_and_album_delete_when_out_of_order_preserve_exact_deltas_and_tombstone(tmp_path: Path) -> None:
    # Given: a persisted file whose current path is known to the workflow.
    client, session = _client(tmp_path)
    old_path = tmp_path / 'incoming' / 'Artist' / 'Release' / 'old.flac'
    new_path = tmp_path / 'incoming' / 'Artist' / 'Release' / 'new.flac'
    old_path.parent.mkdir(parents=True)
    old_path.write_bytes(b'old source bytes')
    new_path.write_bytes(b'new source bytes')
    group = ReleaseGroupRecord(id='group-1', title='Fixture Group')
    release = ReleaseRecord(id='release-1', release_group=group, title='Fixture Release')
    track = TrackRecord(id='track-1', release=release, position=1, title='Fixture Track')
    session.add(ReleaseFileRecord(id='file-1', track=track, relative_path=str(old_path), content_sha256='a' * 64))
    session.commit()

    # When: Rename is delivered before the file exists at its new path, then AlbumDelete is replayed.
    rename = client.post(
        '/api/intake/lidarr',
        json={
            'eventType': 'Rename',
            'renamedTrackFiles': [{'previousPath': str(old_path), 'path': str(new_path)}],
        },
    )
    album_delete = {'eventType': 'AlbumDelete', 'album': {'id': 'release-1'}, 'deletedFiles': True}
    deleted = client.post('/api/intake/lidarr', json=album_delete)
    replayed = client.post('/api/intake/lidarr', json=album_delete)

    # Then: exact paths are retained as a database delta, and deletion only appends one tombstone.
    assert rename.status_code == 202
    assert deleted.status_code == 202
    assert replayed.status_code == 202
    renamed_file = session.get(ReleaseFileRecord, 'file-1')
    assert renamed_file is not None
    assert renamed_file.relative_path == str(new_path)
    assert session.scalars(select(TombstoneRecord)).one().release_file_id == 'file-1'
    assert len(session.scalars(select(WebhookReceiptRecord)).all()) == 2


def test_lidarr_download_when_malformed_or_outside_incoming_root_rejects_without_a_job(tmp_path: Path) -> None:
    # Given: malformed and outside-root untrusted payloads.
    client, session = _client(tmp_path)
    outside = tmp_path / 'outside.flac'
    outside.write_bytes(b'outside')
    symlink = tmp_path / 'incoming' / 'linked.flac'
    symlink.symlink_to(outside)

    # When: the endpoint receives invalid source input.
    malformed = client.post('/api/intake/lidarr', json={'eventType': 'Download', 'trackFiles': []})
    out_of_root = client.post('/api/intake/lidarr', json=_download(outside))
    via_symlink = client.post('/api/intake/lidarr', json=_download(symlink))
    missing = client.post('/api/intake/lidarr', json=_download(tmp_path / 'incoming' / 'missing.flac'))

    # Then: neither input produces a receipt or job that could misleadingly imply success.
    assert malformed.status_code == 422
    assert out_of_root.status_code == 422
    assert via_symlink.status_code == 422
    assert missing.status_code == 422
    assert session.scalars(select(JobRecord)).all() == []
    assert session.scalars(select(WebhookReceiptRecord)).all() == []


def test_lidarr_download_when_delivered_concurrently_reuses_one_job_and_receipt(tmp_path: Path) -> None:
    # Given: one mounted source and sixteen synchronized webhook deliveries.
    client, session = _client(tmp_path)
    source = tmp_path / 'incoming' / 'Artist' / 'Release' / '01.flac'
    source.parent.mkdir(parents=True)
    source.write_bytes(b'raw source bytes')
    payload = _download(source)
    barrier = Barrier(16)

    # When: all deliveries reach the API at the same time through independent clients.
    def post_download(_: int) -> int:
        barrier.wait()
        with TestClient(client.app, raise_server_exceptions=False) as concurrent_client:
            return concurrent_client.post('/api/intake/lidarr', json=payload).status_code

    with ThreadPoolExecutor(max_workers=16) as executor:
        statuses = list(executor.map(post_download, range(16)))

    # Then: every delivery is accepted and the durable workflow retains one replay-safe job and receipt.
    assert statuses == [202] * 16
    assert len(session.scalars(select(JobRecord)).all()) == 1
    assert len(session.scalars(select(WebhookReceiptRecord)).all()) == 1
