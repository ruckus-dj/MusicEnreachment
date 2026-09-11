from __future__ import annotations

from pathlib import Path

from music_ingest.lyrics.validate import (
    LrcWriteRejected,
    LrcWriteRequest,
    LrcWritten,
    SyncedLyricsInvalid,
    SyncedLyricsValid,
    lrc_output_path,
    validate_synced_lyrics,
    write_lrc_atomically,
)


def test_validate_synced_lyrics_when_payload_is_ordered_utf8_returns_text() -> None:
    # Given: a provider synced payload with in-duration cues.
    payload = b'[00:00.00]First line\n[00:04.50]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 10)

    # Then: the decoded text is exposed with its original timing preserved.
    assert isinstance(result, SyncedLyricsValid)
    assert result.text.startswith('[00:00.00]First line')
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 4.5


def test_validate_synced_lyrics_accepts_a_long_intro() -> None:
    # Given: a track whose first cue arrives long after onset, which no cue-onset rule may reject.
    payload = b'[00:45.00]Late first line\n[00:52.00]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 300)

    # Then: the in-duration long intro is accepted as-is.
    assert isinstance(result, SyncedLyricsValid)
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 52.0


def test_validate_synced_lyrics_accepts_a_long_outro() -> None:
    # Given: a track whose last cue lands ten minutes in, followed by a long instrumental outro.
    payload = b'[00:05.00]Opening line\n[09:30.00]Closing line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 640)

    # Then: a large trailing gap is not a rejection reason.
    assert isinstance(result, SyncedLyricsValid)
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 570.0


def test_validate_synced_lyrics_ignores_metadata_tags() -> None:
    # Given: a payload carrying ordinary LRC metadata tags around two timed lyric lines.
    payload = b'[ar:Fixture Artist]\n[ti:Fixture Track]\n[offset:-500]\n[00:01.00]First line\n[00:02.00]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 30)

    # Then: metadata is not counted as a timed lyric line and does not shift the reported last timestamp.
    assert isinstance(result, SyncedLyricsValid)
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 2.0


def test_validate_synced_lyrics_accepts_repeated_timestamps() -> None:
    # Given: two distinct lyric lines published at the same timestamp.
    payload = b'[00:10.00]First line\n[00:10.00]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 20)

    # Then: equal timestamps are accepted rather than read as an ordering failure.
    assert isinstance(result, SyncedLyricsValid)
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 10.0


def test_validate_synced_lyrics_accepts_multiple_timestamp_tags_on_one_line() -> None:
    # Given: one repeated chorus text carrying three timestamp tags.
    payload = b'[00:10.00][00:20.00][00:30.00]Repeated chorus\n[00:11.00]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 60)

    # Then: the multi-tagged line counts once and every tag contributes to the last timestamp.
    assert isinstance(result, SyncedLyricsValid)
    assert result.timed_line_count == 2
    assert result.last_timestamp_seconds == 30.0


def test_validate_synced_lyrics_when_cues_move_backwards_rejects() -> None:
    # Given: cues whose leading timestamps move backwards, which breaks nondecreasing line order.
    payload = b'[00:20.00]Later line\n[00:10.00]Earlier line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 60)

    # Then: the out-of-order payload is rejected rather than published as synced text.
    assert result == SyncedLyricsInvalid('lyric timestamps must not move backwards between lines')


def test_validate_synced_lyrics_when_timestamp_exceeds_duration_rejects() -> None:
    # Given: a synced payload whose cues run past the published audio duration.
    payload = b'[00:00.00]First line\n[00:30.00]Too late\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 10)

    # Then: the payload is rejected and never becomes a sidecar.
    assert result == SyncedLyricsInvalid('lyric timestamp is outside the published audio duration')


def test_validate_synced_lyrics_when_timestamp_is_negative_rejects() -> None:
    # Given: a synced payload carrying a negative timestamp.
    payload = b'[-00:30.00]Before the track\n[00:05.00]First line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 10)

    # Then: a timestamp below zero is outside the published duration.
    assert result == SyncedLyricsInvalid('lyric timestamp is outside the published audio duration')


def test_validate_synced_lyrics_when_payload_is_not_utf8_rejects() -> None:
    # Given: malformed provider lyric bytes.
    payload = b'\xff\xfe'

    # When: the parser receives non-UTF-8 text.
    result = validate_synced_lyrics(payload, 10)

    # Then: only valid UTF-8 may reach a published sidecar.
    assert result == SyncedLyricsInvalid('synced lyrics are not valid UTF-8')


def test_validate_synced_lyrics_when_a_single_timed_line_is_present_rejects() -> None:
    # Given: a synced payload with only one timed lyric line.
    payload = b'[00:00.00]Only line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 10)

    # Then: a single cue is not enough evidence to expose lyrics.
    assert result == SyncedLyricsInvalid('lyric sidecar must contain at least two timed lyric lines')


def test_validate_synced_lyrics_when_a_line_has_no_timestamp_rejects() -> None:
    # Given: a payload mixing timed cues with an untimed lyric line.
    payload = b'[00:00.00]First line\nUntimed line\n[00:04.00]Second line\n'

    # When: the payload crosses the managed-sidecar validation boundary.
    result = validate_synced_lyrics(payload, 10)

    # Then: an untimed lyric line cannot be published as synced text.
    assert result == SyncedLyricsInvalid('lyric sidecar contains a line without a leading timestamp')


def test_write_lrc_atomically_when_long_intro_text_is_validated_publishes_sidecar(tmp_path: Path) -> None:
    # Given: validated provider text for a published audio file inside the managed media root.
    media_root = tmp_path / 'media'
    audio_path = media_root / 'Fixture Release' / 'Fixture Track.flac'
    audio_path.parent.mkdir(parents=True)
    audio_path.write_bytes(b'audio')
    text = '[00:45.00]Late first line\n[00:52.00]Second line\n'
    validated = validate_synced_lyrics(text.encode('utf-8'), 300)

    # When: the validated text is published next to the audio file.
    assert isinstance(validated, SyncedLyricsValid)
    result = write_lrc_atomically(LrcWriteRequest(media_root, audio_path, validated.text))

    # Then: the sidecar is written verbatim with no additional timing policy applied.
    assert isinstance(result, LrcWritten)
    assert result.path == audio_path.with_suffix('.lrc')
    assert result.relative_path == 'Fixture Release/Fixture Track.lrc'
    assert result.path.read_text(encoding='utf-8') == text


def test_write_lrc_atomically_replaces_an_existing_sidecar(tmp_path: Path) -> None:
    # Given: an existing sidecar whose content no longer matches the validated provider text.
    media_root = tmp_path / 'media'
    audio_path = media_root / 'Fixture Release' / 'Fixture Track.flac'
    audio_path.parent.mkdir(parents=True)
    audio_path.write_bytes(b'audio')
    sidecar = audio_path.with_suffix('.lrc')
    _ = sidecar.write_text('[00:00.00]Stale line\n[00:01.00]Stale line\n', encoding='utf-8')
    text = '[00:05.00]First line\n[09:30.00]Closing line\n'

    # When: the replacement write runs.
    result = write_lrc_atomically(LrcWriteRequest(media_root, audio_path, text))

    # Then: the previous sidecar is superseded in place without imposing legacy cue rules.
    assert isinstance(result, LrcWritten)
    assert sidecar.read_text(encoding='utf-8') == text


def test_write_lrc_atomically_when_audio_is_outside_the_media_root_rejects(tmp_path: Path) -> None:
    # Given: a published audio path that escapes the managed media root.
    media_root = tmp_path / 'media'
    media_root.mkdir(parents=True)
    outside = tmp_path / 'escape.flac'

    # When: the sidecar path is resolved for that audio file.
    result = write_lrc_atomically(LrcWriteRequest(media_root, outside, '[00:00.00]First line\n'))

    # Then: containment is enforced before any write is attempted.
    assert result == LrcWriteRejected('published audio is outside the managed media root')


def test_lrc_output_path_when_audio_is_outside_the_media_root_raises(tmp_path: Path) -> None:
    # Given: an existing managed media root and a traversal-shaped audio path.
    media_root = tmp_path / 'media'
    media_root.mkdir(parents=True)

    # When: the sidecar path is resolved outside the managed media root.
    try:
        _ = lrc_output_path(media_root, tmp_path / 'escape.flac')
    except ValueError as error:
        # Then: containment is enforced before any write is attempted.
        assert str(error) == 'published audio is outside the managed media root'
    else:
        raise AssertionError('audio outside the managed media root was accepted')


def test_lrc_output_path_when_audio_is_the_media_root_raises(tmp_path: Path) -> None:
    # Given: a published audio path equal to the managed media root itself.
    media_root = tmp_path / 'media'
    media_root.mkdir(parents=True)

    # When: the sidecar path is resolved for the media root.
    try:
        _ = lrc_output_path(media_root, media_root)
    except ValueError as error:
        # Then: the managed root is not a publishable audio file.
        assert str(error) == 'published audio is outside the managed media root'
    else:
        raise AssertionError('the managed media root was accepted as audio')
