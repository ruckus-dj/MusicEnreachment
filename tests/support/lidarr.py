from __future__ import annotations

from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import Protocol


class LidarrCommandTransport(Protocol):
    def post(self, url: str, *, headers: dict[str, str], json: dict[str, list[str] | str], timeout: float) -> None: ...


@dataclass(frozen=True, slots=True)
class LidarrClient:
    base_url: str
    api_key: str
    transport: LidarrCommandTransport
    timeout_seconds: float = 10.0


@dataclass(frozen=True, slots=True)
class LidarrIntakeEvent:
    source_path: str
    source_id: str


def enqueue_lidarr_intake(event: LidarrIntakeEvent, client: LidarrClient) -> LidarrIntakeEvent:
    """Retain Lidarr's raw pathname while explicitly scheduling its folder rescan."""
    source_path = PurePosixPath(event.source_path)
    if not source_path.is_absolute() or source_path.parent == source_path:
        raise ValueError('Lidarr intake path must name a file below an absolute folder')
    client.transport.post(
        f'{client.base_url.rstrip("/")}/api/v1/command',
        headers={'X-Api-Key': client.api_key},
        json={'name': 'RescanFolders', 'folders': [str(source_path.parent)]},
        timeout=client.timeout_seconds,
    )
    return event
