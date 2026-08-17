from __future__ import annotations

import os
import shutil
import tempfile
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from typing import Final, override

from music_ingest.inspectors.media_capabilities import inspect_media_capability
from music_ingest.models import SourceRecord
from music_ingest.normalize.tags import MetadataTagError, read_normalized_tags
from music_ingest.source_boundary import SourceBoundaryError, resolve_owned_source

_AUDIO_SUFFIXES: Final = frozenset(
    {'.aac', '.aiff', '.alac', '.ape', '.flac', '.m4a', '.mp3', '.ogg', '.opus', '.wav', '.wma'}
)
_REQUIRED_TAGS: Final = frozenset({'ARTIST', 'ALBUM', 'GENRE'})
_MAX_ARTWORK_BYTES: Final = 20 * 1024 * 1024


@dataclass(frozen=True, slots=True)
class PublicationRequest:
    staged_release: Path
    staging_root: Path
    media_root: Path
    source_paths: tuple[Path, ...]
    ffmpeg_command: str = 'ffmpeg'
    timeout_seconds: float = 30.0
    require_canonical_tags: bool = True
    destination_release: Path | None = None
    replace_existing: bool = False
    destination_audio_name: str | None = None
    replace_artwork: bool = False
    sources: tuple[SourceRecord, ...] = ()


@dataclass(frozen=True, slots=True)
class PublicationResult:
    published_release: Path
    published_audio: Path | None = None


@dataclass(frozen=True, slots=True)
class PublicationError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def publish_release(request: PublicationRequest) -> PublicationResult:
    """Validate an isolated staged release before atomically exposing it to media."""
    source_snapshots = _source_snapshots(request.source_paths, request.sources)
    recovered = _recover_completed_publication(request)
    if recovered is not None:
        return recovered
    staged_release, staging_root, media_root = _controlled_roots(request)
    relative_release = staged_release.relative_to(staging_root)
    audio_paths = _validate_release(staged_release, request)
    _reject_source_hardlinks(audio_paths, source_snapshots)
    target_name = request.destination_audio_name or audio_paths[0].name
    published_release = _destination_release(request, media_root, relative_release)
    if published_release.exists() and not published_release.is_dir():
        raise PublicationError('media destination is not a directory')
    published_release.parent.mkdir(parents=True, exist_ok=True)
    temporary_release = Path(tempfile.mkdtemp(prefix=f'.{published_release.name}.', dir=published_release.parent))
    try:
        if published_release.exists():
            shutil.copytree(published_release, temporary_release, dirs_exist_ok=True)
        _merge_release(staged_release, temporary_release, request)
        copied_audio = _validate_release(temporary_release, request)
        _reject_source_hardlinks(copied_audio, source_snapshots)
        _replace_release(temporary_release, published_release)
        _fsync_directory(published_release.parent)
    except OSError as error:
        shutil.rmtree(temporary_release, ignore_errors=True)
        raise PublicationError('atomic release publication failed') from error
    except PublicationError as error:
        shutil.rmtree(temporary_release, ignore_errors=True)
        raise error
    shutil.rmtree(staged_release)
    return PublicationResult(published_release=published_release, published_audio=published_release / target_name)


def replace_published_audio(request: PublicationRequest, target_audio: Path) -> PublicationResult:
    """Atomically replace one published track and its staged artwork."""
    request.media_root.mkdir(parents=True, exist_ok=True)
    staged_release, _, media_root = _controlled_roots(request)
    target = target_audio.resolve()
    if target.parent != media_root.resolve() and media_root.resolve() not in target.parents:
        raise PublicationError('published audio is outside the media root')
    source_snapshots = _source_snapshots(request.source_paths, request.sources)
    audio_paths = _validate_release(staged_release, request)
    if len(audio_paths) != 1 or target.suffix.casefold() not in _AUDIO_SUFFIXES:
        raise PublicationError('republish requires one supported audio target')
    _reject_source_hardlinks(audio_paths, source_snapshots)
    target.parent.mkdir(parents=True, exist_ok=True)
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


def _merge_release(source: Path, destination: Path, request: PublicationRequest) -> None:
    """Overlay one staged track onto an existing album directory."""
    audio_paths = tuple(
        path for path in source.rglob('*') if path.is_file() and path.suffix.casefold() in _AUDIO_SUFFIXES
    )
    if len(audio_paths) != 1:
        raise PublicationError('a publication must contain exactly one audio track')
    staged_audio = audio_paths[0]
    target_name = request.destination_audio_name or staged_audio.name
    for existing in destination.iterdir() if destination.exists() else ():
        if (
            existing.is_file()
            and existing.suffix.casefold() in _AUDIO_SUFFIXES
            and existing.stem == Path(target_name).stem
        ):
            existing.unlink()
    for path in source.rglob('*'):
        relative = path.relative_to(source)
        target = destination / (target_name if path == staged_audio else relative)
        if path.is_dir():
            target.mkdir(parents=True, exist_ok=True)
        elif path.is_file():
            if (
                path.name.casefold() in {'cover.jpg', 'cover.webp'}
                and not request.replace_artwork
                and any((destination / name).exists() for name in ('cover.jpg', 'cover.webp'))
            ):
                continue
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


def _source_snapshots(
    paths: tuple[Path, ...], sources: tuple[SourceRecord, ...]
) -> tuple[tuple[Path, int, int, str], ...]:
    if not paths:
        raise PublicationError('publication requires immutable source provenance')
    if len(paths) != len(sources):
        raise PublicationError('publication requires persisted source ownership')
    snapshots: list[tuple[Path, int, int, str]] = []
    for raw_path, source in zip(paths, sources, strict=True):
        try:
            source_path = resolve_owned_source(source)
        except SourceBoundaryError as error:
            raise PublicationError(f'publication source violates persisted root boundary: {error}') from error
        if source_path != raw_path.resolve(strict=True):
            raise PublicationError('publication source path does not match persisted provenance')
        snapshot = source_path.stat()
        snapshots.append((source_path, snapshot.st_dev, snapshot.st_ino, _sha256(source_path)))
    return tuple(snapshots)


def _validate_release(release: Path, request: PublicationRequest) -> tuple[Path, ...]:
    if any(path.is_symlink() for path in release.rglob('*')):
        raise PublicationError('staged release cannot contain symbolic links')
    paths = tuple(path for path in release.rglob('*') if path.is_file())
    audio_paths = tuple(path for path in paths if path.is_file() and path.suffix.casefold() in _AUDIO_SUFFIXES)
    if not audio_paths:
        raise PublicationError('release has no supported audio')
    artwork = tuple(path for path in paths if path.name.casefold() in {'cover.jpg', 'cover.webp'})
    if len(artwork) > 1:
        raise PublicationError('release must contain at most one external cover')
    if artwork:
        _validate_artwork(artwork[0])
    for audio_path in audio_paths:
        _validate_capability(audio_path, request)
        _validate_tags(audio_path, request)
    for lyric_path in (path for path in paths if path.suffix.casefold() == '.lrc'):
        _validate_lrc(lyric_path)
    return audio_paths


def _validate_capability(path: Path, request: PublicationRequest) -> None:
    inspection = inspect_media_capability(path, timeout_seconds=request.timeout_seconds)
    if inspection.capability is None:
        raise PublicationError('audio has no declared publication capability')


def _validate_tags(path: Path, request: PublicationRequest) -> None:
    try:
        tags = dict(read_normalized_tags(path))
    except MetadataTagError as error:
        raise PublicationError('Mutagen metadata validation failed') from error
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
