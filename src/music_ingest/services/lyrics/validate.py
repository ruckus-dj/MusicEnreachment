from __future__ import annotations

import os
import re
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from tempfile import NamedTemporaryFile
from typing import Final

_TIMED_LINE: Final = re.compile(r'^(?P<stamps>(?:\[-?\d{2,}:[0-5]\d(?:[.:]\d{1,3})?\])+)(?P<text>.*)$')
_TIMESTAMP: Final = re.compile(
    r'\[(?P<sign>-?)(?P<minutes>\d{2,}):(?P<seconds>[0-5]\d)(?:[.:](?P<fraction>\d{1,3}))?\]'
)
_METADATA_LINE: Final = re.compile(r'^\[[A-Za-z][A-Za-z0-9_]*:[^\]]*\]$')

_INVALID_UTF8: Final = 'synced lyrics are not valid UTF-8'
_UNSUPPORTED_LINE: Final = 'lyric sidecar contains a line without a leading timestamp'
_NO_TIMED_LINES: Final = 'lyric sidecar must contain at least two timed lyric lines'
_TIMESTAMP_OUTSIDE_DURATION: Final = 'lyric timestamp is outside the published audio duration'
_TIMESTAMPS_NOT_MONOTONIC: Final = 'lyric timestamps must not move backwards between lines'


@dataclass(frozen=True, slots=True)
class SyncedLyricsValid:
    """Accepted provider payload: validated text plus the evidence used to accept it."""

    text: str
    timed_line_count: int
    last_timestamp_seconds: float


@dataclass(frozen=True, slots=True)
class SyncedLyricsInvalid:
    reason: str


type SyncedLyricsValidation = SyncedLyricsValid | SyncedLyricsInvalid


@dataclass(frozen=True, slots=True)
class LrcWriteRequest:
    """Validated sidecar text bound to the published audio file it belongs to."""

    media_root: Path
    audio_path: Path
    text: str


@dataclass(frozen=True, slots=True)
class LrcWritten:
    path: Path
    relative_path: str
    sha256: str


@dataclass(frozen=True, slots=True)
class LrcWriteRejected:
    reason: str


type LrcWriteResult = LrcWritten | LrcWriteRejected


def validate_synced_lyrics(payload: bytes, duration_seconds: float) -> SyncedLyricsValidation:
    """Validate a provider synced-lyrics payload before it becomes a managed sidecar.

    Provider text is untrusted input. Standard LRC metadata tags (``[ar:...]``, ``[ti:...]``, ``[offset:...]``)
    and lines carrying several timestamp tags are permitted; every lyric timestamp must fall inside the published
    audio duration, cue timestamps must not move backwards from one lyric line to the next, and at least two timed
    lyric lines must be present. Cue spacing and how late the first cue arrives are not constrained: the published
    duration and the leading cue of each line are the only timing authorities.
    """
    try:
        lyrics = payload.decode('utf-8')
    except UnicodeDecodeError:
        return SyncedLyricsInvalid(_INVALID_UTF8)
    return validate_synced_text(lyrics, duration_seconds)


def validate_synced_text(lyrics: str, duration_seconds: float) -> SyncedLyricsValidation:
    """Validate already-decoded synced lyric text against a published audio duration.

    Metadata tags and timed lines without text are ignored rather than counted, so ``timed_line_count`` counts
    lines that actually carry lyric text: a line carrying several timestamps counts once. Line order is checked on
    each line's leading timestamp: equal timestamps and later timestamps are accepted, a leading timestamp that
    moves backwards is a rejection, and a timestamp below zero or beyond the published duration is a timing
    rejection.
    """
    timed_line_count = 0
    last_timestamp = 0.0
    previous_lead: float | None = None
    for line in lyrics.splitlines():
        stripped = line.strip()
        if not stripped or _METADATA_LINE.match(stripped) is not None:
            continue
        match = _TIMED_LINE.match(stripped)
        if match is None:
            return SyncedLyricsInvalid(_UNSUPPORTED_LINE)
        if not match.group('text').strip():
            continue
        timestamps = _timestamps(match.group('stamps'))
        if any(timestamp < 0.0 or timestamp > duration_seconds for timestamp in timestamps):
            return SyncedLyricsInvalid(_TIMESTAMP_OUTSIDE_DURATION)
        if previous_lead is not None and timestamps[0] < previous_lead:
            return SyncedLyricsInvalid(_TIMESTAMPS_NOT_MONOTONIC)
        previous_lead = timestamps[0]
        timed_line_count += 1
        last_timestamp = max(last_timestamp, *timestamps)
    if timed_line_count < 2:
        return SyncedLyricsInvalid(_NO_TIMED_LINES)
    return SyncedLyricsValid(lyrics, timed_line_count, last_timestamp)


def lrc_output_path(media_root: Path, audio_path: Path) -> Path:
    """Resolve the sidecar path for one published audio file inside the managed media root."""
    root = media_root.resolve(strict=True)
    audio = audio_path.resolve()
    stem = audio.stem
    if audio.parent == root or root not in audio.parents:
        raise ValueError('published audio is outside the managed media root')
    if not stem or Path(stem).name != stem or audio.name != f'{stem}{audio.suffix}':
        raise ValueError('published audio name is not a safe filename')
    return audio.with_suffix('.lrc')


def write_lrc_atomically(request: LrcWriteRequest) -> LrcWriteResult:
    """Replace the managed sidecar with already-validated text using a same-directory atomic replace.

    The previous sidecar stays readable until the replacement is complete, so an interrupted write never
    publishes a partial file. Writing imposes no timing policy of its own: the caller passes text that has
    already crossed ``validate_synced_lyrics``, so no legacy cue rule can reject it here.
    """
    try:
        directory = request.media_root.resolve(strict=True)
        output = lrc_output_path(directory, request.audio_path)
    except ValueError as error:
        return LrcWriteRejected(str(error))
    except OSError as error:
        return LrcWriteRejected(f'managed media root is unavailable: {error}')
    payload = request.text.encode('utf-8')
    temporary: Path | None = None
    try:
        with NamedTemporaryFile(dir=output.parent, prefix=f'.{output.name}.', suffix='.tmp', delete=False) as handle:
            temporary = Path(handle.name)
            _ = handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, output)
        temporary = None
        _fsync_directory(output.parent)
    except OSError as error:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        return LrcWriteRejected(f'lyric sidecar could not be written: {error}')
    return LrcWritten(output, str(output.relative_to(directory)), sha256(payload).hexdigest())


def _timestamps(stamps: str) -> tuple[float, ...]:
    timestamps: list[float] = []
    for match in _TIMESTAMP.finditer(stamps):
        fraction_text = match.group('fraction')
        seconds = int(match.group('minutes')) * 60 + int(match.group('seconds'))
        if fraction_text:
            seconds += float(f'0.{fraction_text}')
        timestamps.append(-seconds if match.group('sign') else seconds)
    return tuple(timestamps)


def _fsync_directory(directory: Path) -> None:
    descriptor = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
