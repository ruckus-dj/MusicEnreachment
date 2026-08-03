from __future__ import annotations

import json
import os
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from hashlib import sha256
from pathlib import Path
from subprocess import TimeoutExpired, run
from typing import Final, override

_AUDIO_SUFFIXES: Final = frozenset({'.flac'})
_REQUIRED_TAGS: Final = frozenset({'ARTIST', 'ALBUM', 'GENRE'})
_MAX_ARTWORK_BYTES: Final = 20 * 1024 * 1024


@dataclass(frozen=True, slots=True)
class PublicationRequest:
    staged_release: Path
    staging_root: Path
    media_root: Path
    retention_root: Path
    source_paths: tuple[Path, ...]
    retention_days: int = 30
    flac_command: str = 'flac'
    metaflac_command: str = 'metaflac'
    timeout_seconds: float = 30.0


@dataclass(frozen=True, slots=True)
class PublicationResult:
    published_release: Path
    rollback_manifest: Path


@dataclass(frozen=True, slots=True)
class PublicationError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def publish_release(request: PublicationRequest) -> PublicationResult:
    """Validate an isolated staged release before atomically exposing it to media."""
    recovered = _recover_completed_publication(request)
    if recovered is not None:
        return recovered
    staged_release, staging_root, media_root, retention_root = _controlled_roots(request)
    relative_release = staged_release.relative_to(staging_root)
    source_snapshots = _source_snapshots(request.source_paths)
    audio_paths = _validate_release(staged_release, request)
    _reject_source_hardlinks(audio_paths, source_snapshots)
    published_release = media_root / relative_release
    if published_release.exists():
        raise PublicationError('media destination already exists')
    manifest_path = _write_manifest(retention_root, relative_release, source_snapshots, request.retention_days)
    published_release.parent.mkdir(parents=True, exist_ok=True)
    try:
        os.replace(staged_release, published_release)
        _fsync_directory(published_release.parent)
    except OSError as error:
        raise PublicationError('atomic release publication failed') from error
    return PublicationResult(published_release=published_release, rollback_manifest=manifest_path)


def _recover_completed_publication(request: PublicationRequest) -> PublicationResult | None:
    staging_root = request.staging_root.resolve(strict=True)
    media_root = request.media_root.resolve(strict=True)
    retention_root = request.retention_root.resolve(strict=True)
    staged_release = request.staged_release.resolve()
    if staged_release == staging_root or staging_root not in staged_release.parents:
        raise PublicationError('release must be nested under controlled staging')
    if staged_release.exists():
        return None
    relative_release = staged_release.relative_to(staging_root)
    published_release = media_root / relative_release
    manifest_path = retention_root / f'{sha256(str(relative_release).encode()).hexdigest()}.rollback.json'
    if published_release.is_dir() and manifest_path.is_file():
        return PublicationResult(published_release=published_release, rollback_manifest=manifest_path)
    raise PublicationError('staged release is missing')


def _controlled_roots(request: PublicationRequest) -> tuple[Path, Path, Path, Path]:
    staging_root = request.staging_root.resolve(strict=True)
    media_root = request.media_root.resolve(strict=True)
    retention_root = request.retention_root.resolve(strict=True)
    staged_release = request.staged_release.resolve(strict=True)
    if not all(path.is_dir() for path in (staging_root, media_root, retention_root, staged_release)):
        raise PublicationError('publication roots and staged release must be directories')
    if staged_release == staging_root or staging_root not in staged_release.parents:
        raise PublicationError('release must be nested under controlled staging')
    if staging_root.stat().st_dev != media_root.stat().st_dev:
        raise PublicationError('staging and media must share a filesystem')
    if request.retention_days < 1:
        raise PublicationError('retention must be at least one day')
    return staged_release, staging_root, media_root, retention_root


def _source_snapshots(paths: tuple[Path, ...]) -> tuple[tuple[Path, int, int, str], ...]:
    if not paths:
        raise PublicationError('publication requires immutable source provenance')
    snapshots: list[tuple[Path, int, int, str]] = []
    for raw_path in paths:
        source_path = raw_path.resolve(strict=True)
        if not source_path.is_file():
            raise PublicationError('publication source must be a file')
        snapshot = source_path.stat()
        snapshots.append((source_path, snapshot.st_dev, snapshot.st_ino, _sha256(source_path)))
    return tuple(snapshots)


def _validate_release(release: Path, request: PublicationRequest) -> tuple[Path, ...]:
    if any(path.is_symlink() for path in release.rglob('*')):
        raise PublicationError('staged release cannot contain symbolic links')
    paths = tuple(path for path in release.rglob('*') if path.is_file())
    audio_paths = tuple(path for path in paths if path.suffix.casefold() in _AUDIO_SUFFIXES)
    if not audio_paths:
        raise PublicationError('release has no supported audio')
    artwork = tuple(path for path in paths if path.name.casefold() in {'cover.jpg', 'cover.webp'})
    if len(artwork) != 1:
        raise PublicationError('release must contain exactly one external cover')
    _validate_artwork(artwork[0])
    for audio_path in audio_paths:
        _validate_flac(audio_path, request)
        _validate_tags(audio_path, request)
    for lyric_path in (path for path in paths if path.suffix.casefold() == '.lrc'):
        _validate_lrc(lyric_path)
    return audio_paths


def _validate_flac(path: Path, request: PublicationRequest) -> None:
    try:
        completed = run(  # noqa: S603
            (request.flac_command, '--test', str(path)),
            capture_output=True,
            check=False,
            text=True,
            timeout=request.timeout_seconds,
        )
    except (FileNotFoundError, TimeoutExpired, OSError) as error:
        raise PublicationError('FLAC validation tool failed') from error
    if completed.returncode != 0:
        raise PublicationError('FLAC validation failed')


def _validate_tags(path: Path, request: PublicationRequest) -> None:
    try:
        completed = run(  # noqa: S603
            (request.metaflac_command, '--export-tags-to=-', str(path)),
            capture_output=True,
            check=False,
            text=True,
            timeout=request.timeout_seconds,
        )
    except (FileNotFoundError, TimeoutExpired, OSError) as error:
        raise PublicationError('metadata validation tool failed') from error
    if completed.returncode != 0:
        raise PublicationError('metadata validation failed')
    tags = {
        line.split('=', maxsplit=1)[0]: line.split('=', maxsplit=1)[1]
        for line in completed.stdout.splitlines()
        if '=' in line
    }
    if not _REQUIRED_TAGS.issubset(tags):
        raise PublicationError('canonical publication tags are incomplete')
    for name in ('ARTIST', 'GENRE'):
        if not _semicolon_list(tags[name]):
            raise PublicationError(f'canonical {name.lower()} tags must use semicolon-separated values')


def _semicolon_list(value: str) -> bool:
    return all(part.strip() for part in value.split(';'))


def _validate_artwork(path: Path) -> None:
    payload = path.read_bytes()
    valid_jpeg = (
        path.suffix.casefold() == '.jpg' and payload.startswith(b'\xff\xd8\xff') and payload.endswith(b'\xff\xd9')
    )
    valid_webp = (
        path.suffix.casefold() == '.webp' and len(payload) >= 12 and payload[:4] == b'RIFF' and payload[8:12] == b'WEBP'
    )
    if not (0 < len(payload) <= _MAX_ARTWORK_BYTES and (valid_jpeg or valid_webp)):
        raise PublicationError('release artwork is invalid')


def _validate_lrc(path: Path) -> None:
    try:
        text = path.read_text(encoding='utf-8')
    except UnicodeDecodeError as error:
        raise PublicationError('lyric sidecar is not UTF-8') from error
    if not text.strip():
        raise PublicationError('lyric sidecar is empty')


def _reject_source_hardlinks(audio_paths: tuple[Path, ...], sources: tuple[tuple[Path, int, int, str], ...]) -> None:
    source_inodes = {(device, inode) for _, device, inode, _ in sources}
    if any((path.stat().st_dev, path.stat().st_ino) in source_inodes for path in audio_paths):
        raise PublicationError('published media cannot hardlink to source downloads')


def _write_manifest(
    retention_root: Path, relative_release: Path, sources: tuple[tuple[Path, int, int, str], ...], retention_days: int
) -> Path:
    release_key = sha256(str(relative_release).encode()).hexdigest()
    manifest_path = retention_root / f'{release_key}.rollback.json'
    payload = {
        'release': str(relative_release),
        'retain_until': (datetime.now(UTC) + timedelta(days=retention_days)).isoformat(),
        'sources': [
            {'path': str(path), 'device': device, 'inode': inode, 'sha256': digest}
            for path, device, inode, digest in sources
        ],
    }
    try:
        with manifest_path.open('x', encoding='utf-8') as manifest:
            _ = manifest.write(json.dumps(payload, sort_keys=True, separators=(',', ':')) + '\n')
            manifest.flush()
            os.fsync(manifest.fileno())
    except FileExistsError as error:
        raise PublicationError('rollback manifest already exists') from error
    return manifest_path


def _sha256(path: Path) -> str:
    digest = sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def _fsync_directory(path: Path) -> None:
    try:
        descriptor = os.open(path, os.O_RDONLY)
    except OSError as error:
        raise PublicationError('media directory cannot be synchronized') from error
    try:
        os.fsync(descriptor)
    except OSError as error:
        raise PublicationError('media directory synchronization failed') from error
    finally:
        os.close(descriptor)
