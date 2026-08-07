from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
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
    source_paths: tuple[Path, ...]
    flac_command: str = 'flac'
    metaflac_command: str = 'metaflac'
    timeout_seconds: float = 30.0
    require_canonical_tags: bool = True
    destination_release: Path | None = None
    replace_existing: bool = False


@dataclass(frozen=True, slots=True)
class PublicationResult:
    published_release: Path


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
    staged_release, staging_root, media_root = _controlled_roots(request)
    relative_release = staged_release.relative_to(staging_root)
    source_snapshots = _source_snapshots(request.source_paths)
    audio_paths = _validate_release(staged_release, request)
    _reject_source_hardlinks(audio_paths, source_snapshots)
    published_release = _destination_release(request, media_root, relative_release)
    if published_release.exists() and not request.replace_existing:
        raise PublicationError('media destination already exists')
    published_release.parent.mkdir(parents=True, exist_ok=True)
    temporary_release = Path(tempfile.mkdtemp(prefix=f'.{published_release.name}.', dir=published_release.parent))
    try:
        _copy_release(staged_release, temporary_release)
        copied_audio = _validate_release(temporary_release, request)
        _reject_source_hardlinks(copied_audio, source_snapshots)
        _replace_release(temporary_release, published_release)
        _fsync_directory(published_release.parent)
    except OSError as error:
        shutil.rmtree(temporary_release, ignore_errors=True)
        raise PublicationError('atomic release publication failed') from error
    except PublicationError:
        shutil.rmtree(temporary_release, ignore_errors=True)
        raise
    shutil.rmtree(staged_release)
    return PublicationResult(published_release=published_release)


def replace_published_audio(request: PublicationRequest, target_audio: Path) -> PublicationResult:
    """Atomically replace one published FLAC with a validated staged FLAC."""
    staged_release, _, media_root = _controlled_roots(request)
    target = target_audio.resolve()
    if target.parent != media_root.resolve() and media_root.resolve() not in target.parents:
        raise PublicationError('published audio is outside the media root')
    source_snapshots = _source_snapshots(request.source_paths)
    audio_paths = _validate_release(staged_release, request)
    if len(audio_paths) != 1 or target.suffix.casefold() != '.flac':
        raise PublicationError('republish requires one FLAC target')
    _reject_source_hardlinks(audio_paths, source_snapshots)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f'.{target.name}.', dir=target.parent)
    os.close(descriptor)
    temporary_path = Path(temporary_name)
    try:
        shutil.copy2(audio_paths[0], temporary_path)
        os.replace(temporary_path, target)
        _fsync_directory(target.parent)
    except OSError as error:
        temporary_path.unlink(missing_ok=True)
        raise PublicationError('atomic audio replacement failed') from error
    shutil.rmtree(staged_release)
    return PublicationResult(published_release=target.parent)


def _recover_completed_publication(request: PublicationRequest) -> PublicationResult | None:
    staging_root = request.staging_root.resolve(strict=True)
    media_root = request.media_root.resolve(strict=True)
    staged_release = request.staged_release.resolve()
    if staged_release == staging_root or staging_root not in staged_release.parents:
        raise PublicationError('release must be nested under controlled staging')
    relative_release = staged_release.relative_to(staging_root)
    published_release = _destination_release(request, media_root, relative_release)
    if staged_release.exists():
        return None
    if published_release.is_dir() and request.replace_existing:
        return PublicationResult(published_release=published_release)
    raise PublicationError('staged release is missing')


def _controlled_roots(request: PublicationRequest) -> tuple[Path, Path, Path]:
    staging_root = request.staging_root.resolve(strict=True)
    media_root = request.media_root.resolve(strict=True)
    staged_release = request.staged_release.resolve(strict=True)
    if not all(path.is_dir() for path in (staging_root, media_root, staged_release)):
        raise PublicationError('publication roots and staged release must be directories')
    if staged_release == staging_root or staging_root not in staged_release.parents:
        raise PublicationError('release must be nested under controlled staging')
    return staged_release, staging_root, media_root


def _destination_release(request: PublicationRequest, media_root: Path, relative_release: Path) -> Path:
    destination = (request.destination_release or media_root / relative_release).resolve()
    if destination == media_root or media_root not in destination.parents:
        raise PublicationError('media destination is outside the media root')
    if destination.is_symlink():
        raise PublicationError('media destination cannot be a symlink')
    return destination


def _copy_release(source: Path, destination: Path) -> None:
    for path in source.rglob('*'):
        relative = path.relative_to(source)
        target = destination / relative
        if path.is_dir():
            target.mkdir()
        elif path.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)


def _replace_release(temporary_release: Path, destination: Path) -> None:
    backup: Path | None = None
    if destination.exists():
        backup = destination.with_name(f'.{destination.name}.previous')
        if backup.exists():
            raise PublicationError('media destination replacement backup already exists')
        os.replace(destination, backup)
    try:
        os.replace(temporary_release, destination)
    except OSError:
        if backup is not None and not destination.exists():
            os.replace(backup, destination)
        raise
    if backup is not None:
        shutil.rmtree(backup)


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
    if len(artwork) > 1:
        raise PublicationError('release must contain at most one external cover')
    if artwork:
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
    if request.require_canonical_tags and not _REQUIRED_TAGS.issubset(tags):
        raise PublicationError('canonical publication tags are incomplete')
    for name in ('ARTIST', 'GENRE') if request.require_canonical_tags else ():
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
