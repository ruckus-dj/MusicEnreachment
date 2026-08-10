from __future__ import annotations

import re
from dataclasses import dataclass
from hashlib import sha256
from os import link, replace
from pathlib import Path
from time import monotonic, sleep
from uuid import uuid4

import httpx2 as httpx
from pydantic import BaseModel

type JsonValue = str | int | bool | None | list['JsonValue'] | dict[str, 'JsonValue']


@dataclass(frozen=True, slots=True)
class FileBaseline:
    path: str
    device: int
    inode: int
    links: int
    mtime_ns: int
    sha256: str


@dataclass(frozen=True, slots=True)
class LidarrCommand:
    identifier: int
    status: str


class CommandResponse(BaseModel):
    id: int
    status: str


def capture(path: Path) -> FileBaseline:
    state = path.stat()
    return FileBaseline(
        path=str(path),
        device=state.st_dev,
        inode=state.st_ino,
        links=state.st_nlink,
        mtime_ns=state.st_mtime_ns,
        sha256=sha256(path.read_bytes()).hexdigest(),
    )


def atomic_reconcile(incoming: Path, media: Path) -> tuple[FileBaseline, FileBaseline]:
    """Replace exactly one existing incoming pathname with a same-directory temporary hardlink."""
    before = capture(incoming)
    published = capture(media)
    if before.device != published.device:
        raise RuntimeError('incoming and media are not on one filesystem')
    temporary = incoming.with_name(f'.{incoming.name}.task-12-{uuid4().hex}')
    link(media, temporary)
    try:
        replace(temporary, incoming)
    finally:
        if temporary.exists():
            temporary.unlink()
    after = capture(incoming)
    if after.path != before.path or after.inode != published.inode or after.sha256 != published.sha256:
        raise RuntimeError('atomic pathname reconciliation did not preserve the required media identity')
    return before, after


class LidarrApi:
    def __init__(self, base_url: str, api_key: str) -> None:
        self._base_url: str = base_url.rstrip('/')
        self._headers: dict[str, str] = {'X-Api-Key': api_key}

    def get(self, endpoint: str) -> str:
        with httpx.Client(timeout=10.0) as client:
            response = client.get(f'{self._base_url}{endpoint}', headers=self._headers)
            _ = response.raise_for_status()
            return response.text

    def command(self, name: str, payload: dict[str, str | int | list[str]]) -> LidarrCommand:
        with httpx.Client(timeout=10.0) as client:
            response = client.post(
                f'{self._base_url}/api/v1/command', headers=self._headers, json={'name': name, **payload}
            )
            _ = response.raise_for_status()
            result = CommandResponse.model_validate_json(response.text)
        return LidarrCommand(result.id, result.status)

    def wait(self, command: LidarrCommand, timeout_seconds: float = 30.0) -> LidarrCommand:
        deadline = monotonic() + timeout_seconds
        while monotonic() < deadline:
            result = CommandResponse.model_validate_json(self.get(f'/api/v1/command/{command.identifier}'))
            status = result.status
            if status == 'completed':
                return LidarrCommand(command.identifier, status)
            if status in {'failed', 'aborted', 'cancelled'}:
                raise RuntimeError(f'Lidarr command {command.identifier} ended with {status}')
            sleep(0.1)
        raise RuntimeError(f'Lidarr command {command.identifier} did not complete within {timeout_seconds:g} seconds')


def lifecycle_preconditions(
    lidarr_url: str, stand_root: Path, enabled: bool
) -> tuple[tuple[str, ...], dict[str, JsonValue]]:
    if not enabled:
        return ('preflight rejected lifecycle execution',), {}
    if '${LIDARR_IMAGE' not in (stand_root / 'compose.yaml').read_text(encoding='utf-8'):
        return ('unsupported controlled immutable upgrade: compose has no task-scoped image override',), {}
    try:
        key = _api_key(stand_root)
        with httpx.Client(timeout=10.0) as client:
            status = client.get(f'{lidarr_url.rstrip("/")}/api/v1/system/status', headers={'X-Api-Key': key})
            _ = status.raise_for_status()
            indexers = client.get(f'{lidarr_url.rstrip("/")}/api/v1/indexer', headers={'X-Api-Key': key})
            _ = indexers.raise_for_status()
    except (FileNotFoundError, httpx.HTTPError, OSError) as error:
        return (f'unsupported local Lidarr API assertion: {error}',), {}
    captures: dict[str, JsonValue] = {'system_status': status.text, 'indexers': indexers.text}
    if indexers.text.strip() == '[]':
        return (
            ('unsupported monitored-release/RSS assertion: test stand has no disposable local Lidarr indexer',),
            captures,
        )
    return ('unsupported managed-fixture assertion: no task-owned Lidarr import workflow is configured',), captures


def _api_key(stand_root: Path) -> str:
    config = (stand_root / 'config/lidarr/config.xml').read_text(encoding='utf-8')
    match = re.search(r'<ApiKey>([^<]+)</ApiKey>', config)
    if match is None or not match.group(1):
        raise OSError('Lidarr API key is unavailable')
    return match.group(1)
