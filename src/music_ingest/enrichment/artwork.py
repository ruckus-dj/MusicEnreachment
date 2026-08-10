from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import Final, Protocol, override

_MAX_ARTWORK_BYTES: Final = 20 * 1024 * 1024


class ArtworkFormat(StrEnum):
    JPEG = 'jpg'
    WEBP = 'webp'


@dataclass(frozen=True, slots=True)
class ArtworkCandidate:
    release_id: str
    format: ArtworkFormat
    payload: bytes


class ArtworkProvider(Protocol):
    def fetch_artwork(self, release_id: str) -> ArtworkCandidate | None: ...


@dataclass(frozen=True, slots=True)
class ArtworkWriteRequest:
    staging_directory: Path
    release_directory: Path
    verified_release_id: str
    candidate: ArtworkCandidate


@dataclass(frozen=True, slots=True)
class ArtworkWriteError(Exception):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def write_release_artwork(request: ArtworkWriteRequest) -> Path:
    """Write one verified release-art sidecar without touching FLAC metadata."""
    staging_directory = request.staging_directory.resolve(strict=True)
    release_directory = request.release_directory.resolve(strict=True)
    if staging_directory not in release_directory.parents:
        raise ArtworkWriteError('release artwork must be inside controlled staging')
    if request.candidate.release_id != request.verified_release_id:
        raise ArtworkWriteError('artwork is not linked to the selected release')
    if len(request.candidate.payload) > _MAX_ARTWORK_BYTES or not _valid_artwork(request.candidate):
        raise ArtworkWriteError('artwork payload has an invalid image envelope')
    names = ('cover.jpg', 'cover.webp')
    if any((release_directory / name).exists() for name in names):
        raise ArtworkWriteError('release already has artwork')
    output_path = release_directory / f'cover.{request.candidate.format.value}'
    try:
        with output_path.open('xb') as artwork_file:
            _ = artwork_file.write(request.candidate.payload)
    except FileExistsError as error:
        raise ArtworkWriteError('release already has artwork') from error
    return output_path


def _valid_artwork(candidate: ArtworkCandidate) -> bool:
    match candidate.format:
        case ArtworkFormat.JPEG:
            return candidate.payload.startswith(b'\xff\xd8\xff') and candidate.payload.endswith(b'\xff\xd9')
        case ArtworkFormat.WEBP:
            return (
                len(candidate.payload) >= 12 and candidate.payload[:4] == b'RIFF' and candidate.payload[8:12] == b'WEBP'
            )
