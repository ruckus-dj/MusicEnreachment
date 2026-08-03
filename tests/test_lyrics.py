from __future__ import annotations

from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.lyrics.validate import (
    LrcCandidate,
    LrcIdentity,
    LyricsAccepted,
    LyricsRejected,
    LyricsWriteRequest,
    persist_lyric_review,
    validate_and_write_lrc,
)
from music_ingest.persistence.models import Base, ReviewDecisionRecord, SourceRecord


def _identity() -> LrcIdentity:
    return LrcIdentity('release-id', 'recording-id', 'Fixture Track', ('Fixture Artist',), 10)


def test_validate_and_write_lrc_when_identity_duration_and_timing_match_publishes_utf8_sidecar(tmp_path: Path) -> None:
    # Given: a finalized identity and ordered UTF-8 cues within the audio duration.
    release = tmp_path / 'staging' / 'Fixture Release'
    release.mkdir(parents=True)
    candidate = LrcCandidate(_identity(), b'[00:00.00]First line\n[00:04.00]Second line\n[00:08.00]Third line\n')

    # When: the external candidate crosses the LRC validation boundary.
    result = validate_and_write_lrc(LyricsWriteRequest(release, _identity(), candidate))

    # Then: the accepted sidecar is external, UTF-8, and named for the track.
    assert isinstance(result, LyricsAccepted)
    assert result.output_path == release / 'Fixture Track.lrc'
    assert result.output_path.read_text(encoding='utf-8').startswith('[00:00.00]')


def test_validate_and_write_lrc_when_anacondaz_offset_cues_fail_creates_review_evidence(
    tmp_path: Path,
) -> None:
    # Given: deliberately offset Anacondaz-style cues that start implausibly late for a ten-second track.
    release = tmp_path / 'staging' / 'Anacondaz'
    release.mkdir(parents=True)
    candidate = LrcCandidate(_identity(), b'[00:30.00]Anacondaz\n[00:35.00]Offset lyrics\n')

    # When: validation evaluates the offset external text.
    result = validate_and_write_lrc(LyricsWriteRequest(release, _identity(), candidate))

    # Then: no LRC publishes and the project review seam persists the rejection.
    assert isinstance(result, LyricsRejected)
    assert not (release / 'Fixture Track.lrc').exists()
    engine = create_engine('sqlite+pysqlite:///:memory:')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            SourceRecord(
                id='source-id',
                source_path='fixture.flac',
                device=1,
                inode=2,
                size_bytes=3,
                sha256='a' * 64,
                duration_seconds=10,
                origin='manual',
                intake_state='needs_review',
            )
        )  # noqa: E501
        session.commit()
        persist_lyric_review(session, 'source-id', result)
        assert session.query(ReviewDecisionRecord).one().rationale == result.reason


def test_validate_and_write_lrc_when_bytes_are_not_utf8_rejects_without_sidecar(tmp_path: Path) -> None:
    # Given: malformed external lyric bytes for a known track identity.
    release = tmp_path / 'staging' / 'Fixture Release'
    release.mkdir(parents=True)
    candidate = LrcCandidate(_identity(), b'\xff\xfe')

    # When: the parser receives non-UTF-8 text.
    result = validate_and_write_lrc(LyricsWriteRequest(release, _identity(), candidate))

    # Then: malformed lyric input cannot publish a sidecar.
    assert result == LyricsRejected('lyric sidecar is not UTF-8')
    assert tuple(release.iterdir()) == ()
