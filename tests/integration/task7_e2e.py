from __future__ import annotations

import shutil
from dataclasses import dataclass
from pathlib import Path
from sys import argv, path
from typing import cast
from uuid import uuid4

import httpx
from pydantic import TypeAdapter

source_root = str(Path(__file__).parents[2] / 'src')
if source_root not in path:
    path.insert(0, source_root)

from music_ingest.integrations.navidrome import ScanResponse  # noqa: E402

from ._task7_e2e_support import (  # noqa: E402
    E2eFailure,
    JsonObject,
    JsonValue,
    compose,
    create_fixture,
    get,
    post,
    query,
    snapshot,
    sql_literal,
    wait_for,
    write_evidence,
)


@dataclass(frozen=True, slots=True)
class Arguments:
    stand_root: Path
    api_url: str
    navidrome_url: str
    evidence: Path


def parse_arguments(raw_arguments: tuple[str, ...] | None = None) -> Arguments:
    values = tuple(argv[1:]) if raw_arguments is None else raw_arguments
    root = Path(__file__).parents[2]
    return Arguments(
        stand_root=Path(option('--stand-root', str(root / 'test_stand'), values)),
        api_url=option('--api-url', 'http://localhost:8787', values),
        navidrome_url=option('--navidrome-url', 'http://localhost:4533', values),
        evidence=Path(option('--evidence', str(root / '.omo/evidence/music-ingest-platform/task-7-e2e.json'), values)),
    )


def option(name: str, default: str, values: tuple[str, ...]) -> str:
    try:
        index = values.index(name)
    except ValueError:
        return default
    if index + 1 == len(values):
        raise E2eFailure(f'{name} requires a value')
    return values[index + 1]


def main() -> None:
    arguments = parse_arguments()
    stand_root = arguments.stand_root.resolve(strict=True)
    arguments.evidence.unlink(missing_ok=True)
    fixture_root = stand_root / 'data' / 'incoming' / f'task-7-{uuid4().hex}'
    source = fixture_root / 'Task Seven Artist' / 'Task Seven Album' / '01 - Task Seven Fixture.flac'
    container_source = Path('/data') / source.relative_to(stand_root / 'data')
    cleanup: dict[str, JsonValue] = {'compose_reset': False, 'fixture_removed': False, 'stack_removed': False}
    evidence: dict[str, JsonValue] | None = None
    job_id = ''
    try:
        _ = compose(stand_root, 'down', '--volumes', '--remove-orphans')
        _ = compose(stand_root, 'up', '--build', '--detach', timeout=600.0)
        cleanup['compose_reset'] = True
        with httpx.Client(base_url=arguments.api_url, timeout=30.0) as client:
            wait_for('music-ingest health', lambda: health_ready(client))
        create_fixture(source)
        source_before = snapshot(source)
        with httpx.Client(base_url=arguments.api_url, timeout=30.0) as client:
            health = get(client, '/healthz', 200)
            test_response = client.post('/api/intake/lidarr', json={'eventType': 'Test', 'instanceName': 'task-7'})
            if test_response.status_code != 204:
                raise E2eFailure(f'Test returned {test_response.status_code}: {test_response.text}')
            download_payload: dict[str, JsonValue] = {
                'eventType': 'Download',
                'instanceName': 'task-7',
                'trackFiles': [{'path': str(container_source)}],
                'isUpgrade': False,
                'deletedFiles': [],
            }
            first_download = post(client, '/api/intake/lidarr', download_payload, 202)
            replay = post(client, '/api/intake/lidarr', download_payload, 202)
            job_id = required_string(first_download, 'job_id')
            if job_id != required_string(replay, 'job_id') or replay.get('replayed') is not True:
                raise E2eFailure('Download replay did not reuse the durable job')
            try:
                wait_for(
                    'Download job completion',
                    lambda: (
                        query(
                            stand_root,
                            ''.join(('select state from jobs where id = ', sql_literal(job_id))),
                        )
                        == 'completed'
                    ),
                )
            except E2eFailure as error:
                states = query(stand_root, "select id || ':' || state from jobs order by created_at")
                attempts = query(
                    stand_root,
                    "select job_id || ':' || attempt_number || ':' || state from job_attempts order by id",
                )
                logs = compose(stand_root, 'logs', '--no-color', 'music-ingest').stdout
                error_details = f'{error}; job states: {states}; attempts: {attempts}'
                raise E2eFailure(f'{error_details}\nmusic-ingest logs:\n{logs}') from error
            release_id = query(
                stand_root,
                ''.join(('select id from releases where id = ', sql_literal(f'{job_id}:release'))),
            )
            if not release_id:
                raise E2eFailure('worker did not persist a workflow release')
            rename = post(
                client,
                '/api/intake/lidarr',
                {
                    'eventType': 'Rename',
                    'renamedTrackFiles': [
                        {'previousPath': str(container_source), 'path': str(container_source.with_name('renamed.flac'))}
                    ],
                },
                202,
            )
            detail = get(client, f'/api/release-review/releases/{release_id}', 200)
            queue = get(client, '/api/release-review/queue', 200)
            edit_response = client.patch(
                f'/api/release-review/releases/{release_id}/tags',
                json={
                    'revision': 1,
                    'tags': {'TITLE': 'Task Seven Edited', 'ARTIST': 'Task Seven Artist', 'GENRE': 'Hip Hop'},
                },
            )
            if edit_response.status_code != 200:
                raise E2eFailure(f'edit returned {edit_response.status_code}: {edit_response.text}')
            edit: JsonObject = cast(JsonObject, TypeAdapter(JsonObject).validate_json(edit_response.content))
            if edit.get('revision') != 2:
                raise E2eFailure('published fallback edit did not create revision two')
            republish = post(client, f'/api/release-review/releases/{release_id}/republish', {'revision': 2}, 200)
            album_delete = post(
                client,
                '/api/intake/lidarr',
                {'eventType': 'AlbumDelete', 'album': {'id': release_id}, 'deletedFiles': True},
                202,
            )
            album_delete_replay = post(
                client,
                '/api/intake/lidarr',
                {'eventType': 'AlbumDelete', 'album': {'id': release_id}, 'deletedFiles': True},
                202,
            )
            if album_delete.get('job_id') != album_delete_replay.get('job_id'):
                raise E2eFailure('AlbumDelete replay did not reuse the durable job')
            wait_for('AlbumDelete tombstone', lambda: query(stand_root, 'select count(*) from tombstones') == '1')
            navidrome = navidrome_visible(arguments.navidrome_url, stand_root)
        source_after = snapshot(source)
        receipts = query(stand_root, 'select count(*) from webhook_receipts')
        jobs = query(stand_root, 'select count(*) from jobs')
        final_revision = query(
            stand_root,
            ''.join(
                (
                    'select max(revision) from tag_layers where release_file_id = ',
                    sql_literal(f'{job_id}:file'),
                    " and layer = 'final'",
                )
            ),
        )
        if source_before != source_after:
            raise E2eFailure('source changed during webhook processing')
        evidence = {
            'task': '7',
            'status': 'confirmed',
            'command': 'uv run python -m tests.integration.task7_e2e',
            'runtime': {
                'health': health,
                'webhook_proxy': 'lidarr -> http://music-ingest:8000/api/intake/lidarr',
            },
            'webhooks': {
                'test_status': 204,
                'rename_job_id': rename.get('job_id'),
                'download_job_id': job_id,
                'download_replay': replay.get('replayed'),
                'album_delete_job_id': album_delete.get('job_id'),
            },
            'postgres': {
                'receipts': int(receipts),
                'jobs': int(jobs),
                'release_id': release_id,
                'final_revision': int(final_revision),
                'tombstones': 1,
            },
            'review': {'queue': queue, 'before_edit': detail, 'edit': edit, 'republish': republish},
            'publication': {'automatic_fallback': True, 'navidrome': navidrome},
            'source_immutability': {
                'before': {'inode': source_before.inode, 'sha256': source_before.sha256},
                'after': {'inode': source_after.inode, 'sha256': source_after.sha256},
                'preserved': True,
            },
            'cleanup': cleanup,
            'constraints': {
                'auth_added': False,
                'approval_added': False,
                'source_mutated': False,
                'hardlinks_added': False,
                'transcoding_added': False,
                'deletion_added': False,
                'public_provider_calls': False,
            },
        }
    finally:
        shutil.rmtree(fixture_root, ignore_errors=True)
        if job_id:
            for root in ('media', '.publish-staging', 'retention'):
                shutil.rmtree(stand_root / 'data' / root / job_id, ignore_errors=True)
        cleanup['fixture_removed'] = not fixture_root.exists()
        _ = compose(stand_root, 'down', '--volumes', '--remove-orphans')
        cleanup['stack_removed'] = True
    write_evidence(arguments.evidence, evidence)


def navidrome_visible(url: str, stand_root: Path) -> dict[str, JsonValue]:
    credentials = dict(
        line.split('=', 1) for line in (stand_root / '.env').read_text(encoding='utf-8').splitlines() if '=' in line
    )
    parameters = {
        'u': credentials['LOCAL_ADMIN_USERNAME'],
        'p': credentials['LOCAL_ADMIN_PASSWORD'],
        'v': '1.16.1',
        'c': 'task-7',
        'f': 'json',
    }
    with httpx.Client(base_url=url, timeout=30.0) as client:
        response = client.post('/rest/startScan', params={**parameters, 'fullScan': 'true'})
        if response.status_code != 200:
            raise E2eFailure(f'Navidrome scan returned {response.status_code}: {response.text}')
        scan: dict[str, JsonValue] = {'status': response.status_code}
        wait_for('Navidrome scan', lambda: scan_complete(get(client, '/rest/getScanStatus', 200, parameters)))
        search = get(
            client,
            '/rest/search3',
            200,
            {**parameters, 'query': 'Task Seven Artist', 'artistCount': '20', 'albumCount': '20', 'songCount': '20'},
        )
    return {'scan': scan, 'search': search}


def required_string(value: dict[str, JsonValue], key: str) -> str:
    result = value.get(key)
    if not isinstance(result, str):
        raise E2eFailure(f'{key} is missing from API response')
    return result


def scan_complete(payload: dict[str, JsonValue]) -> bool:
    return not ScanResponse.model_validate(payload).payload.status.scanning


def health_ready(client: httpx.Client) -> bool:
    try:
        return client.get('/healthz').status_code == 200
    except httpx.ConnectError:
        return False


if __name__ == '__main__':
    main()
