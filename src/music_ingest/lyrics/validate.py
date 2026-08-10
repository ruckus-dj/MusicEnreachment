from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path
from typing import Final

from sqlalchemy.orm import Session

from music_ingest.models import ReviewDecisionRecord

_LRC_LINE: Final = re.compile(r'^\[(\d{2,}):([0-5]\d)(?:\.(\d{1,3}))?\](.+)$')
_MAX_INITIAL_CUE_SECONDS: Final = 10.0
_MAX_CUE_GAP_SECONDS: Final = 30.0


@dataclass(frozen=True, slots=True)
class LrcIdentity:
    release_id: str
    recording_id: str
    title: str
    artists: tuple[str, ...]
    duration_seconds: int


@dataclass(frozen=True, slots=True)
class LrcCandidate:
    identity: LrcIdentity
    payload: bytes


@dataclass(frozen=True, slots=True)
class LyricsWriteRequest:
    release_directory: Path
    identity: LrcIdentity
    candidate: LrcCandidate


@dataclass(frozen=True, slots=True)
class LyricsAccepted:
    output_path: Path


@dataclass(frozen=True, slots=True)
class LyricsRejected:
    reason: str


type LyricsResult = LyricsAccepted | LyricsRejected


def validate_and_write_lrc(request: LyricsWriteRequest) -> LyricsResult:
    """Validate finalized identity and timing before writing an external UTF-8 LRC."""
    if request.candidate.identity != request.identity:
        return LyricsRejected('lyric identity does not match the finalized track')
    try:
        lyrics = request.candidate.payload.decode('utf-8')
    except UnicodeDecodeError:
        return LyricsRejected('lyric sidecar is not UTF-8')
    timestamps = _timestamps(lyrics)
    rejection = _timing_rejection(timestamps, request.identity.duration_seconds)
    if rejection is not None:
        return LyricsRejected(rejection)
    output_path = _output_path(request.release_directory, request.identity.title)
    try:
        with output_path.open('x', encoding='utf-8', newline='\n') as lyric_file:
            _ = lyric_file.write(lyrics)
    except FileExistsError:
        return LyricsRejected('lyric sidecar already exists')
    return LyricsAccepted(output_path)


def persist_lyric_review(session: Session, source_id: str, rejection: LyricsRejected) -> ReviewDecisionRecord:
    """Record a rejected LRC as durable review evidence through the intake schema."""
    review = ReviewDecisionRecord(source_id=source_id, state='needs_review', rationale=rejection.reason)
    session.add(review)
    session.flush()
    return review


def _timestamps(lyrics: str) -> tuple[float, ...] | None:
    timestamps: list[float] = []
    for line in lyrics.splitlines():
        match = _LRC_LINE.fullmatch(line)
        if match is None:
            return None
        minute_text, second_text, fraction_text, text = match.groups()
        if not text.strip():
            return None
        fraction = float(f'0.{fraction_text}') if fraction_text is not None else 0.0
        timestamps.append(int(minute_text) * 60 + int(second_text) + fraction)
    return tuple(timestamps) if timestamps else None


def _timing_rejection(timestamps: tuple[float, ...] | None, duration_seconds: int) -> str | None:
    if timestamps is None or len(timestamps) < 2:
        return 'lyric sidecar must contain at least two timed lines'
    if timestamps[0] > _MAX_INITIAL_CUE_SECONDS:
        return 'lyric sidecar starts too far from track onset'
    previous = -1.0
    for timestamp in timestamps:
        if timestamp <= previous:
            return 'lyric timestamps are not strictly ordered'
        if timestamp > duration_seconds:
            return 'lyric timestamp exceeds audio duration'
        if previous >= 0 and timestamp - previous > _MAX_CUE_GAP_SECONDS:
            return 'lyric timing has an implausible gap'
        previous = timestamp
    return None


def _output_path(release_directory: Path, title: str) -> Path:
    directory = release_directory.resolve(strict=True)
    path = directory / f'{title}.lrc'
    if path.parent != directory or not title or Path(title).name != title:
        raise ValueError('canonical lyric title is not a safe filename')
    return path
