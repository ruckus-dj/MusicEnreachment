from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path
from uuid import UUID

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event, select
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.contracts import AlbumRemapContextResponse, AlbumRemapSelector
from music_ingest.contracts.api import Release
from music_ingest.models import (
    Base,
    JobRecord,
    LibraryRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceRootRecord,
    SourceTagRecord,
)
from music_ingest.services.album_remap import AlbumRemapConflict, load_context, preview_slots
from music_ingest.services.intake.service import IntakeRequest, Origin, SourceTagObservation, intake_source
from music_ingest.services.library.service import library_album_tracks
from music_ingest.services.matching.providers import MusicBrainzHttpResponse


def _add_source(
    session: Session,
    root: Path,
    name: str,
    tags: tuple[tuple[str, str], ...],
    release_id: str | None = None,
) -> str:
    path = root / f'{name}.flac'
    _ = path.write_bytes(name.encode())
    intake = intake_source(
        session,
        IntakeRequest(
            source_path=path,
            source_root_id='root',
            origin=Origin.MANUAL,
            duration_seconds=180,
            tag_observations=tuple(
                SourceTagObservation(format_name='flac', tag_name=tag_name, value=value) for tag_name, value in tags
            ),
            artwork_observations=(),
            provider_attempts=(),
            candidates=(),
            review_decisions=(),
        ),
    )
    source = session.get(SourceRecord, intake.source_id)
    assert source is not None and source.library_record_id is not None
    if release_id is not None:
        record = session.get(LibraryRecord, source.library_record_id)
        assert record is not None
        record.musicbrainz_recording_id = f'recording-{name}'
        record.musicbrainz_release_id = release_id
    return intake.source_id


def test_album_remap_context_when_tag_scoped_matches_library_album_tracks(tmp_path: Path) -> None:
    # Given: release-wide, title-scoped, and missing-tag sources with varied artist tags.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-membership.db"}')
    Base.metadata.create_all(engine)
    release_id = '11111111-1111-4111-8111-111111111111'
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path=str(tmp_path),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        title_source = _add_source(
            session,
            tmp_path,
            'albumartist',
            (('ALBUMARTIST', 'Artist; Guest'), ('ARTIST', 'Other'), ('ALBUM', 'Album')),
        )
        release_source = _add_source(
            session,
            tmp_path,
            'release',
            (('ALBUMARTIST', 'Artist'), ('ALBUM', 'Album')),
            release_id,
        )
        missing_source = _add_source(session, tmp_path, 'missing', ())
        missing_release_source = _add_source(session, tmp_path, 'missing-release', (), release_id)
        _ = _add_source(session, tmp_path, 'missing-album', (('ALBUM', 'Album'),))
        selected_source = _add_source(
            session,
            tmp_path,
            'selected-artist',
            (('ARTIST', 'Canonical Artist'), ('ARTIST', 'Rejected Artist'), ('ALBUM', 'Selected Album')),
        )
        for observation in session.scalars(select(SourceTagRecord).where(SourceTagRecord.source_id == selected_source)):
            observation.selected = observation.value != 'Rejected Artist'
        session.commit()

    title_selector = AlbumRemapSelector(artist_name='Artist', album_name='Album')
    global_title_selector = AlbumRemapSelector(album_name='Album')
    missing_selector = AlbumRemapSelector(artist_missing=True, album_missing=True)
    release_selector = AlbumRemapSelector(release_mbid=UUID(release_id))
    artist_release_selector = AlbumRemapSelector(release_mbid=UUID(release_id), artist_name='Artist')
    with Session(engine) as session:
        # When: each selector is evaluated by the catalog and album-remap context.
        title_context = load_context(session, title_selector)
        global_title_context = load_context(session, global_title_selector)
        missing_context = load_context(session, missing_selector)
        release_context = load_context(session, release_selector)
        artist_release_context = load_context(session, artist_release_selector)
        title_tracks = library_album_tracks(session, 'Artist', album_name='Album')
        global_title_tracks = library_album_tracks(session, None, album_name='Album')
        missing_tracks = library_album_tracks(session, None, artist_missing=True, album_missing=True)
        artist_release_tracks = library_album_tracks(session, 'Artist', album_id=release_id)
        canonical_context = load_context(
            session, AlbumRemapSelector(artist_name='Canonical Artist', album_name='Selected Album')
        )
        rejected_context = load_context(
            session, AlbumRemapSelector(artist_name='Rejected Artist', album_name='Selected Album')
        )
        canonical_tracks = library_album_tracks(session, 'Canonical Artist', album_name='Selected Album')
        rejected_tracks = library_album_tracks(session, 'Rejected Artist', album_name='Selected Album')

    # Then: both surfaces expose exactly the same source membership.
    assert (
        {source.source_id for source in title_context.sources}
        == {track.source_id for track in title_tracks}
        == {title_source}
    )
    assert (
        {source.source_id for source in missing_context.sources}
        == {track.source_id for track in missing_tracks}
        == {missing_source}
    )
    assert {source.source_id for source in global_title_context.sources} == {
        track.source_id for track in global_title_tracks
    }
    assert {source.source_id for source in release_context.sources} == {release_source, missing_release_source}
    assert (
        {source.source_id for source in artist_release_context.sources}
        == {track.source_id for track in artist_release_tracks}
        == {release_source}
    )
    assert (
        {source.source_id for source in canonical_context.sources}
        == {track.source_id for track in canonical_tracks}
        == {selected_source}
    )
    assert (
        {source.source_id for source in rejected_context.sources}
        == {track.source_id for track in rejected_tracks}
        == set()
    )


def test_album_remap_context_does_not_hydrate_unrelated_source_relationships(tmp_path: Path) -> None:
    # Given: one release-scoped source in a database with the full relationship graph available.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-query-shape.db"}')
    Base.metadata.create_all(engine)
    release_id = '11111111-1111-4111-8111-111111111111'
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path=str(tmp_path),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        _ = _add_source(session, tmp_path, 'release', (('ALBUM', 'Album'),), release_id)
        session.commit()

    statements: list[str] = []

    def capture_statement(
        _connection: object,
        _cursor: object,
        statement: str,
        _parameters: object,
        _context: object,
        _executemany: bool,
    ) -> None:
        statements.append(statement)

    event.listen(engine, 'before_cursor_execute', capture_statement)
    try:
        with Session(engine) as session:
            context = load_context(session, AlbumRemapSelector(release_mbid=UUID(release_id)))
    finally:
        event.remove(engine, 'before_cursor_execute', capture_statement)

    assert len(context.sources) == 1
    assert not any(
        relation in statement
        for statement in statements
        for relation in ('candidate_evidence', 'review_decisions', 'provider_attempts', 'fingerprints')
    )


def test_album_remap_releases_when_musicbrainz_is_disabled_returns_503_without_transport_call(tmp_path: Path) -> None:
    # Given: persisted runtime settings explicitly disable MusicBrainz.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-disabled.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            RuntimeSettingRecord(
                key='providers.musicbrainz.enabled',
                value='false',
                updated_at=datetime.now(UTC),
            )
        )
        session.commit()
    calls: list[str] = []

    class Transport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = headers
            calls.append(url)
            return MusicBrainzHttpResponse(200, b'{"releases": []}')

    client = TestClient(create_app(lambda: Session(engine), musicbrainz_transport=Transport()))

    # When: a release search would otherwise invoke the transport.
    response = client.post('/api/library/album-remaps/releases/search', json={'query': 'Artist Album'})

    # Then: the API refuses the unavailable provider without any network attempt.
    assert response.status_code == 503
    assert calls == []


def test_album_remap_apply_when_no_assignment_rejects_without_artwork_job(tmp_path: Path) -> None:
    # Given: one selected album source and a valid provider release snapshot.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-empty.db"}')
    Base.metadata.create_all(engine)
    release_id = '11111111-1111-4111-8111-111111111111'
    now = datetime.now(UTC)
    with Session(engine) as session:
        session.add(
            SourceRootRecord(
                id='root',
                display_name='root',
                canonical_path=str(tmp_path),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        source_id = _add_source(session, tmp_path, 'track', (('ARTIST', 'Artist'), ('ALBUM', 'Album')))
        session.commit()
    body = json.dumps({'id': release_id, 'title': 'Album', 'media': []}).encode()

    class Transport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(200, body)

    client = TestClient(create_app(lambda: Session(engine), musicbrainz_transport=Transport()))
    selector = {
        'release_mbid': None,
        'artist_name': 'Artist',
        'album_name': 'Album',
        'artist_missing': False,
        'album_missing': False,
    }
    context = client.post('/api/library/album-remaps/context', json={'selector': selector})

    # When: an apply request partitions the source as unmatched without assigning a track.
    response = client.post(
        '/api/library/album-remaps/apply',
        json={
            'selector': selector,
            'album_snapshot_token': context.json()['album_snapshot_token'],
            'release_mbid': release_id,
            'release_snapshot_token': hashlib.sha256(body).hexdigest(),
            'assignments': [],
            'unmatched_source_ids': [source_id],
        },
    )

    # Then: the operation is rejected and has not queued unrelated release artwork.
    assert response.status_code == 409
    with Session(engine) as session:
        assert session.query(JobRecord).count() == 0


def test_album_remap_preview_when_pregap_has_no_track_identity_rejects_it() -> None:
    # Given: MusicBrainz omits an optional track identity from a pregap.
    release = Release.model_validate(
        {
            'id': '11111111-1111-4111-8111-111111111111',
            'title': 'Album',
            'media': [
                {
                    'pregap': {
                        'position': 0,
                        'title': 'Pregap',
                        'recording': {'id': '33333333-3333-4333-8333-333333333333', 'title': 'Pregap'},
                    }
                }
            ],
        }
    )

    # When: remap constructs assignable release slots.
    with pytest.raises(AlbumRemapConflict, match='missing a MusicBrainz track identity'):
        _ = preview_slots(AlbumRemapContextResponse(album_snapshot_token='0' * 64, sources=()), release)
