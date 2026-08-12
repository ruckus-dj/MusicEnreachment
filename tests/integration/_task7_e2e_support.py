from __future__ import annotations

import json
import shutil
from collections.abc import Callable
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from subprocess import CompletedProcess, run
from time import monotonic, sleep
from typing import Final

import httpx2 as httpx
from pydantic import TypeAdapter

from music_ingest.normalize.tags import write_normalized_tags

type JsonValue = None | bool | int | float | str | list[JsonValue] | dict[str, JsonValue]
type JsonObject = dict[str, JsonValue]

_JSON_OBJECT_ADAPTER: Final[TypeAdapter[JsonObject]] = TypeAdapter(JsonObject)


@dataclass(frozen=True, slots=True)
class Snapshot:
    inode: int
    sha256: str


class E2eFailure(RuntimeError):
    pass


def compose(stand_root: Path, *arguments: str, timeout: float = 300.0) -> CompletedProcess[str]:
    return command(
        (  # noqa: S607
            'docker',
            'compose',
            '--project-directory',
            str(stand_root),
            '-f',
            str(stand_root / 'compose.yaml'),
            *arguments,
        ),
        timeout,
    )


def command(arguments: tuple[str, ...], timeout: float = 30.0) -> CompletedProcess[str]:
    completed = run(arguments, capture_output=True, check=False, text=True, timeout=timeout)  # noqa: S603
    if completed.returncode != 0:
        raise E2eFailure(f'command failed ({completed.returncode}): {" ".join(arguments)}\n{completed.stderr}')
    return completed


def snapshot(path: Path) -> Snapshot:
    return Snapshot(path.stat().st_ino, sha256(path.read_bytes()).hexdigest())


def create_fixture(path: Path) -> None:
    ffmpeg = shutil.which('ffmpeg')
    if ffmpeg is None:
        raise E2eFailure('ffmpeg is required for task-7 E2E fixture generation')
    path.parent.mkdir(parents=True)
    _ = command(
        (
            ffmpeg,
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
        )
    )
    _ = write_normalized_tags(
        path,
        (
            ('TITLE', 'Task Seven Fixture'),
            ('ARTIST', 'Task Seven Artist; Task Seven Guest'),
            ('ALBUM', 'Task Seven Album'),
            ('ALBUMARTIST', 'Task Seven Artist; Task Seven Guest'),
            ('DATE', '2026'),
            ('TRACKNUMBER', '1'),
            ('TRACKTOTAL', '1'),
            ('DISCNUMBER', '1'),
            ('DISCTOTAL', '1'),
            ('GENRE', 'Hip Hop; Alternative Rock'),
        ),
    )
    _ = (path.parent / 'cover.jpg').write_bytes(b'\xff\xd8\xfftask-seven\xff\xd9')


def post(client: httpx.Client, path: str, payload: JsonObject, expected_status: int) -> JsonObject:
    response = client.post(path, json=payload)
    if response.status_code != expected_status:
        raise E2eFailure(f'{path} returned {response.status_code}: {response.text}')
    return _JSON_OBJECT_ADAPTER.validate_json(response.content)


def get(client: httpx.Client, path: str, expected_status: int, params: dict[str, str] | None = None) -> JsonObject:
    response = client.get(path, params=params)
    if response.status_code != expected_status:
        raise E2eFailure(f'{path} returned {response.status_code}: {response.text}')
    return _JSON_OBJECT_ADAPTER.validate_json(response.content)


def query(stand_root: Path, statement: str) -> str:
    return compose(
        stand_root,
        'exec',
        '-T',
        'postgres',
        'psql',
        '-U',
        'music_ingest',
        '-d',
        'music_ingest',
        '-At',
        '-c',
        statement,
    ).stdout.strip()


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def wait_for(description: str, predicate: Callable[[], bool], timeout_seconds: float = 45.0) -> None:
    deadline = monotonic() + timeout_seconds
    while not predicate():
        if monotonic() >= deadline:
            raise E2eFailure(f'timed out waiting for {description}')
        sleep(0.2)


def write_evidence(path: Path, evidence: dict[str, JsonValue]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix('.tmp')
    _ = temporary.write_text(json.dumps(evidence, indent=2, sort_keys=True) + '\n', encoding='utf-8')
    _ = temporary.replace(path)
