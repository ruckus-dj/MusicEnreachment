from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

import pytest
from sqlalchemy import Engine, create_engine
from sqlalchemy.orm import Session

from music_ingest.adapters.external.lrclib import (
    LrclibAdapter,
    LrclibHttpResponse,
    LrclibSettings,
    LrclibSettingsHolder,
)
from music_ingest.models import (
    Base,
    FingerprintRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
    SourceRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import append_metadata_revision
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker

NOW = datetime(2026, 9, 11, tzinfo=UTC)
DURATION_SECONDS = 232
TITLE = 'Fixture Track'
ARTIST = 'Fixture Artist'
ALBUM = 'Fixture Album'
LYRIC_LINES = ('Fixture lyric line one', 'Fixture lyric line two')
SYNCED_LYRICS = f'[00:01.00]{LYRIC_LINES[0]}\n[00:05.00]{LYRIC_LINES[1]}\n'
OUT_OF_DURATION_LYRICS = f'[00:01.00]{LYRIC_LINES[0]}\n[05:00.00]{LYRIC_LINES[1]}\n'
PLAIN_LYRICS = 'plain lyrics that must never be written'
AUDIO_RELATIVE_PATH = Path(ARTIST) / ALBUM / '01 - Fixture Track.mka'
SIDECAR_RELATIVE_PATH = str(AUDIO_RELATIVE_PATH.with_suffix('.lrc'))


@dataclass(slots=True)
class RecordingTransport:
    """Offline lrclib transport: it records the lookup URL and can never reach the network."""

    response: LrclibHttpResponse
    calls: list[str] = field(default_factory=list)

    def get(self, url: str, *, headers: dict[str, str]) -> LrclibHttpResponse:
        _ = headers
        self.calls.append(url)
        return self.response


def _provider_response(synced_lyrics: str | None = SYNCED_LYRICS, **overrides: object) -> LrclibHttpResponse:
    payload: dict[str, object] = {
        'id': 4242,
        'trackName': TITLE,
        'artistName': ARTIST,
        'albumName': ALBUM,
        'duration': float(DURATION_SECONDS),
        'instrumental': False,
        'plainLyrics': PLAIN_LYRICS,
        'syncedLyrics': synced_lyrics,
    }
    payload.update(overrides)
    return LrclibHttpResponse(status_code=200, body=json.dumps([payload]).encode())


def _status_response(status_code: int) -> LrclibHttpResponse:
    return LrclibHttpResponse(status_code=status_code, body=b'')


def _engine(tmp_path: Path) -> Engine:
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "lrclib-handler.db"}')
    Base.metadata.create_all(engine)
    return engine


def _published_record(
    session: Session,
    media_root: Path,
    *,
    record_id: str = 'record-lyrics',
    duration_seconds: int | None = DURATION_SECONDS,
    fingerprint_duration: float | None = None,
    audio_relative_path: Path = AUDIO_RELATIVE_PATH,
    publication_id: str = 'publication-current',
    publication_state: str = 'current',
    with_final_metadata: bool = True,
) -> tuple[LibraryRecord, SourceRecord, LibraryPublicationRecord]:
    """Create one record whose current publication points at real bytes inside the managed media root."""
    audio_path = media_root / audio_relative_path
    audio_path.parent.mkdir(parents=True, exist_ok=True)
    _ = audio_path.write_bytes(b'published-audio')
    record = LibraryRecord(id=record_id, created_at=NOW, updated_at=NOW)
    source = SourceRecord(
        id=f'{record_id}-source',
        source_path=str(media_root.parent / 'incoming' / 'fixture.flac'),
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=duration_seconds,
        origin='manual',
        intake_state='stored',
        library_record=record,
    )
    session.add_all((record, source))
    metadata_revision_id: int | None = None
    if with_final_metadata:
        revision = append_metadata_revision(
            session, record.id, source.id, 'final', {'TITLE': TITLE, 'ARTIST': ARTIST, 'ALBUM': ALBUM}, 'test', NOW
        )
        metadata_revision_id = revision.id
    if duration_seconds is None and fingerprint_duration is not None:
        session.add(
            FingerprintRecord(
                source=source,
                state='stored',
                fingerprint='AQAA',
                duration_seconds=fingerprint_duration,
                output_sha256='c' * 64,
            )
        )
    publication = LibraryPublicationRecord(
        id=publication_id,
        library_record=record,
        source=source,
        path=str(audio_path),
        format_name='mka',
        content_sha256='b' * 64,
        metadata_revision_id=metadata_revision_id,
        state=publication_state,
        created_at=NOW,
    )
    session.add(publication)
    session.flush()
    return record, source, publication


def _enqueue(session: Session, record_id: str = 'record-lyrics') -> JobRecord:
    job = JobRepository(session).enqueue_lrclib_fetch(record_id, NOW)
    assert job is not None
    return job


def _run_worker(session: Session, media_root: Path, response: LrclibHttpResponse) -> RecordingTransport:
    """Run the real worker over the queued fetch with an offline adapter injected through the config."""
    transport = RecordingTransport(response)
    config = ProcessingConfig(
        incoming_root=media_root.parent / 'incoming',
        staging_root=media_root.parent / 'staging',
        media_root=media_root,
        lrclib_adapter=LrclibAdapter(transport),
    )
    assert ProcessingWorker(session, config).run_once()
    session.commit()
    return transport


def _lyric_state(session: Session) -> tuple[str, str | None, str | None, str | None, datetime | None]:
    """The materialized lyric state of the record as one unit, so every invariant is checked at once."""
    record = session.get(LibraryRecord, 'record-lyrics')
    assert record is not None
    return (
        record.lyrics_status,
        record.lyrics_path,
        record.lyrics_publication_id,
        record.lyrics_sha256,
        record.lyrics_updated_at,
    )


def _events(session: Session) -> list[LibraryEventRecord]:
    return list(session.query(LibraryEventRecord).order_by(LibraryEventRecord.id))


def _details(event: LibraryEventRecord) -> dict[str, object]:
    return json.loads(event.details_json)


# --- success ---------------------------------------------------------------------------------------------


def test_synced_fetch_writes_validated_sidecar_and_materializes_success(tmp_path: Path) -> None:
    # Given: a record with a current publication, final metadata, and a queued lyrics fetch.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the worker claims the fetch and the mocked provider returns matching synced lyrics.
    with Session(engine) as session:
        transport = _run_worker(session, media_root, _provider_response())

        # Then: the record carries the success state, the sidecar path, current publication, hash, and timestamp.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == 'synced'
        assert path == SIDECAR_RELATIVE_PATH
        assert publication_id == 'publication-current'
        assert digest == sha256(SYNCED_LYRICS.encode()).hexdigest()
        assert updated_at is not None

        # ...and: the sidecar holds exactly the validated text next to the published audio.
        sidecar = media_root / SIDECAR_RELATIVE_PATH
        assert sidecar.read_text() == SYNCED_LYRICS
        assert digest == sha256(sidecar.read_bytes()).hexdigest()

        # ...and: the lookup asked for the published identity and the persisted published duration.
        query = parse_qs(urlsplit(transport.calls[0]).query)
        assert query == {
            'artist_name': [ARTIST],
            'track_name': [TITLE],
            'album_name': [ALBUM],
        }

        # ...and: the fetch completed with one human-history event carrying the outcome evidence.
        job = session.query(JobRecord).filter_by(library_record_id='record-lyrics').one()
        assert job.state == 'completed'
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_synced'
        assert event.state == 'synced'
        assert SIDECAR_RELATIVE_PATH in (event.reason or '')
        details = _details(event)
        assert details['outcome'] == 'synced'
        assert details['publication_id'] == 'publication-current'
        assert details['provider'] == 'lrclib'
        assert details['record_id'] == 4242
        assert details['endpoint'] == 'https://lrclib.net/api/search'
        assert details['http_status'] == 200
        assert details['request_hash'] == sha256(transport.calls[0].encode()).hexdigest()
        assert details['response_sha256'] == sha256(_provider_response().body).hexdigest()
        assert details['sidecar_path'] == SIDECAR_RELATIVE_PATH
        assert details['sidecar_sha256'] == digest


def test_published_duration_falls_back_to_the_newest_fingerprint(tmp_path: Path) -> None:
    # Given: a published source whose own duration is unknown but which carries persisted fingerprint evidence.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root, duration_seconds=None, fingerprint_duration=232.4)
        _ = _enqueue(session)
        session.commit()

    # When: the worker claims the fetch.
    with Session(engine) as session:
        transport = _run_worker(session, media_root, _provider_response())

        # Then: the search is structured by published metadata; duration is enforced locally as a hard gate.
        assert parse_qs(urlsplit(transport.calls[0]).query)['track_name'] == [TITLE]
        assert _lyric_state(session)[0] == 'synced'


# --- no candidate ---------------------------------------------------------------------------------------


def test_no_candidate_clears_sidecar_fields_and_keeps_the_reason(tmp_path: Path) -> None:
    # Given: a record whose current publication has no lyrics at the provider.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the mocked provider answers 404 for the published identity.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _status_response(404))

        # Then: the record is settled as no_candidate with every sidecar field cleared and nothing written.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == 'no_candidate'
        assert (path, publication_id, digest) == (None, None, None)
        assert updated_at is not None
        assert not (media_root / SIDECAR_RELATIVE_PATH).exists()

        # ...and: the event records the provider endpoint, request hash, response hash, and status.
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_no_candidate'
        assert event.state == 'no_candidate'
        assert event.reason == 'lrclib has no lyrics for this track'
        details = _details(event)
        assert details['outcome'] == 'no_candidate'
        assert details['publication_id'] == 'publication-current'
        assert details['endpoint'] == 'https://lrclib.net/api/search'
        assert details['http_status'] == 404
        assert 'request_hash' in details
        assert 'response_sha256' in details
        assert 'sidecar_path' not in details


# --- validation rejection -------------------------------------------------------------------------------


def test_malformed_lrc_is_validation_rejected_without_writing_a_sidecar(tmp_path: Path) -> None:
    # Given: a provider payload whose lyric timestamp falls outside the published audio duration.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the mocked provider returns that malformed synced lyrics payload.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _provider_response(OUT_OF_DURATION_LYRICS))

        # Then: the record is settled as validation_rejected with every sidecar field cleared.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == 'validation_rejected'
        assert (path, publication_id, digest) == (None, None, None)
        assert updated_at is not None
        assert not (media_root / SIDECAR_RELATIVE_PATH).exists()

        # ...and: the event names the validation reason instead of carrying any lyric text.
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_validation_rejected'
        assert event.reason == 'lyric timestamp is outside the published audio duration'
        details = _details(event)
        assert details['outcome'] == 'validation_rejected'
        assert details['validation_reason'] == 'lyric timestamp is outside the published audio duration'
        assert details['record_id'] == 4242


# --- provider error -------------------------------------------------------------------------------------


def test_provider_error_clears_sidecar_fields_and_completes_the_fetch(tmp_path: Path) -> None:
    # Given: a record whose provider lookup will fail inside the provider.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the mocked provider answers 500.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _status_response(500))

        # Then: the failure is terminal and materialized as error with cleared sidecar fields.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == 'error'
        assert (path, publication_id, digest) == (None, None, None)
        assert updated_at is not None
        assert not (media_root / SIDECAR_RELATIVE_PATH).exists()
        assert session.query(JobRecord).filter_by(library_record_id='record-lyrics').one().state == 'completed'

        # ...and: the event keeps the provider status and hashes for later inspection.
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_error'
        assert event.state == 'error'
        assert event.reason == 'lrclib returned unexpected status 500'
        details = _details(event)
        assert details['outcome'] == 'error'
        assert details['http_status'] == 500
        assert details['response_sha256'] == sha256(b'').hexdigest()


# --- sidecar overwrite ----------------------------------------------------------------------------------


def test_existing_sidecar_is_replaced_only_on_success(tmp_path: Path) -> None:
    # Given: a record whose published audio already has a sidecar from an earlier fetch.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    sidecar = media_root / SIDECAR_RELATIVE_PATH
    sidecar.parent.mkdir(parents=True)
    _ = sidecar.write_bytes(b'stale sidecar from an earlier publication\n')
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: a fetch has no candidate at the provider.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _status_response(404))

        # Then: the existing sidecar is left untouched while the record state is cleared.
        assert _lyric_state(session)[0] == 'no_candidate'
        assert sidecar.read_bytes() == b'stale sidecar from an earlier publication\n'

    # When: a later fetch for the same record succeeds.
    with Session(engine) as session:
        _ = _enqueue(session)
        session.commit()
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _provider_response())

        # Then: the sidecar is replaced with exactly the validated text and hashed as written.
        status, path, publication_id, digest, _updated_at = _lyric_state(session)
        assert status == 'synced'
        assert path == SIDECAR_RELATIVE_PATH
        assert publication_id == 'publication-current'
        assert sidecar.read_text() == SYNCED_LYRICS
        assert digest == sha256(SYNCED_LYRICS.encode()).hexdigest()
        assert [event.kind for event in _events(session)] == ['lrclib_fetch_no_candidate', 'lrclib_fetch_synced']


# --- materialized-field invariants ----------------------------------------------------------------------


@pytest.mark.parametrize(
    ('response', 'expected_status', 'expects_sidecar'),
    [
        (_provider_response(), 'synced', True),
        (_provider_response(OUT_OF_DURATION_LYRICS), 'validation_rejected', False),
        (_status_response(404), 'no_candidate', False),
        (_status_response(500), 'error', False),
    ],
    ids=['synced', 'validation_rejected', 'no_candidate', 'error'],
)
def test_materialized_lyric_fields_are_all_or_nothing_per_outcome(
    tmp_path: Path, response: LrclibHttpResponse, expected_status: str, expects_sidecar: bool
) -> None:
    # Given: a record whose published audio sits inside the managed media root.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the worker settles the fetch for one provider outcome.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, response)

        # Then: the status always moves with a timestamp, and the sidecar triple exists only with a sidecar file.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == expected_status
        assert updated_at is not None
        assert (path is not None) is expects_sidecar
        assert (publication_id is not None) is expects_sidecar
        assert (digest is not None) is expects_sidecar
        if expects_sidecar:
            assert path == SIDECAR_RELATIVE_PATH
            assert publication_id == 'publication-current'
            assert digest == sha256((media_root / SIDECAR_RELATIVE_PATH).read_bytes()).hexdigest()
        else:
            assert not (media_root / SIDECAR_RELATIVE_PATH).exists()

        # ...and: exactly one event explains the outcome, and no lyric text is stored anywhere on the record.
        assert len(_events(session)) == 1
        record = session.get(LibraryRecord, 'record-lyrics')
        assert record is not None
        stored = ' '.join(
            str(value)
            for value in (
                record.lyrics_status,
                record.lyrics_path,
                record.lyrics_publication_id,
                record.lyrics_sha256,
            )
        )
        assert all(line not in stored for line in LYRIC_LINES)


# --- lyric text exclusion -------------------------------------------------------------------------------


def test_no_event_reason_or_detail_ever_carries_lyric_text(tmp_path: Path) -> None:
    # Given: a record whose provider body carries plain lyrics, synced lyrics, and the raw body itself.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: one accepted fetch and one rejected fetch are processed.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _provider_response())
    with Session(engine) as session:
        _ = _enqueue(session)
        session.commit()
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _provider_response(OUT_OF_DURATION_LYRICS))

        # Then: no stored event field carries lyric text, the raw body, or a lyric-bearing key.
        events = _events(session)
        assert len(events) == 2
        for event in events:
            serialized = f'{event.kind}\n{event.state}\n{event.reason}\n{event.details_json}'
            assert all(line not in serialized for line in LYRIC_LINES)
            assert PLAIN_LYRICS not in serialized
            assert 'syncedLyrics' not in serialized
            assert _provider_response().body.decode() not in serialized
            assert set(_details(event)) <= {
                'outcome',
                'publication_id',
                'provider',
                'endpoint',
                'request_hash',
                'response_sha256',
                'http_status',
                'record_id',
                'sidecar_path',
                'sidecar_sha256',
                'validation_reason',
            }

        # ...and: the accepted text only ever reached the validated sidecar.
        assert (media_root / SIDECAR_RELATIVE_PATH).read_text() == SYNCED_LYRICS


# --- publication robustness -----------------------------------------------------------------------------


def test_superseded_publication_binds_lyrics_to_the_current_publication(tmp_path: Path) -> None:
    # Given: a record whose earlier publication was superseded by a newer current one.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    superseded_relative = Path(ARTIST) / ALBUM / '01 - Fixture Track.mka'
    current_relative = Path(ARTIST) / ALBUM / '01 - Fixture Track [remastered].mka'
    with Session(engine) as session:
        record, source, superseded = _published_record(
            session, media_root, audio_relative_path=superseded_relative, publication_id='publication-superseded'
        )
        superseded.state = 'superseded'
        current_path = media_root / current_relative
        _ = current_path.write_bytes(b'published-audio-remastered')
        revision = session.query(LibraryMetadataRevisionRecord).filter_by(library_record_id=record.id).one()
        session.add(
            LibraryPublicationRecord(
                id='publication-newer',
                library_record=record,
                source_id=source.id,
                path=str(current_path),
                format_name='mka',
                content_sha256='d' * 64,
                metadata_revision_id=revision.id,
                state='current',
                created_at=NOW.replace(microsecond=1),
            )
        )
        _ = _enqueue(session)
        session.commit()

    # When: the fetch is processed after the supersede.
    with Session(engine) as session:
        _ = _run_worker(session, media_root, _provider_response())

        # Then: lyrics bind to the current publication only, and the superseded sidecar is never written.
        status, path, publication_id, _digest, _updated_at = _lyric_state(session)
        assert status == 'synced'
        assert publication_id == 'publication-newer'
        assert path == str(current_relative.with_suffix('.lrc'))
        assert (media_root / current_relative.with_suffix('.lrc')).read_text() == SYNCED_LYRICS
        assert not (media_root / superseded_relative.with_suffix('.lrc')).exists()


def test_missing_current_publication_settles_as_error_without_writing(tmp_path: Path) -> None:
    # Given: a record whose only publication is no longer current.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root, publication_state='superseded')
        _ = _enqueue(session)
        session.commit()

    # When: the fetch is processed with no current publication to bind lyrics to.
    with Session(engine) as session:
        transport = _run_worker(session, media_root, _provider_response())

        # Then: the fetch settles terminally without calling the provider and without clearing a good state.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert status == 'error'
        assert (path, publication_id, digest) == (None, None, None)
        assert updated_at is not None
        assert transport.calls == []
        assert session.query(JobRecord).filter_by(library_record_id='record-lyrics').one().state == 'completed'
        assert list(media_root.rglob('*.lrc')) == []

        # ...and: the event explains the missing publication instead of blaming the provider.
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_error'
        assert event.reason == 'no current publication to bind synced lyrics to'
        details = _details(event)
        assert details['outcome'] == 'error'
        assert 'endpoint' not in details


def test_published_metadata_without_identity_settles_as_error_without_lookup(tmp_path: Path) -> None:
    # Given: a current publication whose final metadata carries no title or artist.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root, with_final_metadata=False)
        _ = _enqueue(session)
        session.commit()

    # When: the fetch is processed.
    with Session(engine) as session:
        transport = _run_worker(session, media_root, _provider_response())

        # Then: an unidentifiable published track is a terminal error rather than a guessed provider lookup.
        status, path, publication_id, digest, _updated_at = _lyric_state(session)
        assert status == 'error'
        assert (path, publication_id, digest) == (None, None, None)
        assert transport.calls == []
        event = _events(session)[0]
        assert event.kind == 'lrclib_fetch_error'
        assert event.reason == 'published final metadata has no track title and artist'
        assert _details(event)['publication_id'] == 'publication-current'


def test_unknown_published_duration_settles_as_error_without_lookup(tmp_path: Path) -> None:
    # Given: a current publication whose source and fingerprint evidence carry no duration.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root, duration_seconds=None)
        _ = _enqueue(session)
        session.commit()

    # When: the fetch is processed.
    with Session(engine) as session:
        transport = _run_worker(session, media_root, _provider_response())

        # Then: lyrics are never validated against an unknown duration, so this is a terminal error.
        assert _lyric_state(session)[0] == 'error'
        assert transport.calls == []
        assert _events(session)[0].reason == 'published audio duration is unknown'


def test_fetch_is_retried_when_the_adapter_is_not_configured(tmp_path: Path) -> None:
    # Given: a queued fetch in a worker whose config carries no lrclib adapter.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the worker dispatches the job without a provider.
    with Session(engine) as session:
        config = ProcessingConfig(
            incoming_root=tmp_path / 'incoming', staging_root=tmp_path / 'staging', media_root=media_root
        )
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: the missing provider is infrastructure, so the fetch stays retryable instead of settling.
        assert session.query(JobRecord).filter_by(library_record_id='record-lyrics').one().state == 'queued'
        assert [event.kind for event in _events(session)] == ['selection_refresh_retry']
        assert _lyric_state(session)[0] == 'none'


def test_missing_library_record_is_retried(tmp_path: Path) -> None:
    # Given: a queued fetch that targets a record which no longer exists.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    media_root.mkdir(parents=True)
    with Session(engine) as session:
        _ = _enqueue(session, 'record-vanished')
        session.commit()

    # When: the worker dispatches it.
    with Session(engine) as session:
        assert ProcessingWorker(
            session,
            ProcessingConfig(
                incoming_root=tmp_path / 'incoming',
                staging_root=tmp_path / 'staging',
                media_root=media_root,
                lrclib_adapter=LrclibAdapter(RecordingTransport(_provider_response())),
            ),
        ).run_once()
        session.commit()

        # Then: a vanished record is treated as infrastructure rather than a terminal lyric outcome.
        assert session.query(JobRecord).one().state == 'queued'
        assert _events(session) == []


def test_disabled_provider_settles_the_queued_fetch_without_provider_traffic(tmp_path: Path) -> None:
    # Given: a queued fetch for a record whose provider was switched off after the job was queued.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _ = _published_record(session, media_root)
        _ = _enqueue(session)
        session.commit()

    # When: the worker dispatches the fetch with the disabled provider injected through the config.
    with Session(engine) as session:
        transport = RecordingTransport(_provider_response())
        config = ProcessingConfig(
            incoming_root=tmp_path / 'incoming',
            staging_root=tmp_path / 'staging',
            media_root=media_root,
            lrclib_adapter=LrclibAdapter(transport, LrclibSettingsHolder(LrclibSettings(enabled=False))),
        )
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: no lookup reaches the provider, and the fetch settles terminally instead of staying retryable.
        status, path, publication_id, digest, updated_at = _lyric_state(session)
        assert transport.calls == []
        assert session.query(JobRecord).filter_by(library_record_id='record-lyrics').one().state == 'completed'
        assert (status, path, publication_id, digest) == ('none', None, None, None)
        assert updated_at is not None
        events = _events(session)
        assert [event.kind for event in events] == ['lrclib_fetch_skipped']
        assert events[0].state == 'none'
        assert events[0].reason == 'lrclib provider is disabled in runtime settings'


def test_disabled_provider_keeps_lyrics_validated_before_the_switch(tmp_path: Path) -> None:
    # Given: a queued fetch whose record already carries validated lyrics from an earlier enabled run.
    engine = _engine(tmp_path)
    media_root = tmp_path / 'media'
    with Session(engine) as session:
        _published_record(session, media_root)
        record = session.get(LibraryRecord, 'record-lyrics')
        assert record is not None
        record.lyrics_status = 'synced'
        record.lyrics_path = SIDECAR_RELATIVE_PATH
        record.lyrics_publication_id = 'publication-current'
        record.lyrics_sha256 = 'b' * 64
        record.lyrics_updated_at = NOW
        _ = _enqueue(session)
        session.commit()

    # When: the worker dispatches the fetch after the operator switched the provider off.
    with Session(engine) as session:
        transport = RecordingTransport(_provider_response())
        config = ProcessingConfig(
            incoming_root=tmp_path / 'incoming',
            staging_root=tmp_path / 'staging',
            media_root=media_root,
            lrclib_adapter=LrclibAdapter(transport, LrclibSettingsHolder(LrclibSettings(enabled=False))),
        )
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: switching the provider off is not evidence against lyrics it produced, so nothing is rewritten.
        assert transport.calls == []
        assert session.query(JobRecord).filter_by(library_record_id='record-lyrics').one().state == 'completed'
        assert _lyric_state(session)[:4] == ('synced', SIDECAR_RELATIVE_PATH, 'publication-current', 'b' * 64)
        assert _lyric_state(session)[4] == NOW.replace(tzinfo=None)
        assert _events(session) == []
