from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path
from uuid import UUID

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event
from sqlalchemy.orm import Session

from music_ingest.api.app import create_app
from music_ingest.contracts import AlbumRemapApplyRequest, AlbumRemapAssignment, AlbumRemapSelector
from music_ingest.contracts.api import Release
from music_ingest.models import Base, LibraryMetadataRevisionRecord, SourceRecordingAssignmentRecord, SourceRootRecord
from music_ingest.services import album_remap
from music_ingest.services.album_remap import AlbumRemapConflict, AlbumRemapRelease, apply_remap, load_context
from music_ingest.services.intake.service import IntakeRequest, Origin, SourceTagObservation, intake_source
from music_ingest.services.matching.providers import MusicBrainzHttpResponse


def test_album_remap_apply_when_token_is_stale_or_assignment_invalid_rolls_back(tmp_path: Path) -> None:
    # Given: a remap request made against an existing album context.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-apply.db"}')
    Base.metadata.create_all(engine)
    response_body = json.dumps({'id': '11111111-1111-4111-8111-111111111111', 'title': 'Album'}).encode()

    class FixtureTransport:
        def get(self, url: str, *, headers: dict[str, str]) -> MusicBrainzHttpResponse:
            _ = url, headers
            return MusicBrainzHttpResponse(200, response_body)

    client = TestClient(create_app(lambda: Session(engine), musicbrainz_transport=FixtureTransport()))
    selector = {
        'release_mbid': None,
        'artist_name': 'Artist',
        'album_name': 'Album',
        'artist_missing': False,
        'album_missing': False,
    }
    payload = {
        'selector': selector,
        'album_snapshot_token': '0' * 64,
        'release_mbid': '11111111-1111-4111-8111-111111111111',
        'release_snapshot_token': hashlib.sha256(response_body).hexdigest(),
        'assignments': [],
        'unmatched_source_ids': [],
    }

    # When: a stale or structurally incomplete assignment is submitted.
    response = client.post('/api/library/album-remaps/apply', json=payload)

    # Then: the request is rejected before any database mutation can commit.
    assert response.status_code == 409
    assert response.json()['detail'] == 'album snapshot is stale'


def test_album_remap_apply_when_one_assignment_is_invalid_rolls_back_every_source(tmp_path: Path) -> None:
    # Given: two album sources and two selected slots that improperly share one recording identity.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-rollback.db"}')
    Base.metadata.create_all(engine)
    selector = AlbumRemapSelector(artist_name='Artist', album_name='Album')
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
        source_ids: list[str] = []
        for number in (1, 2):
            path = tmp_path / f'{number}.flac'
            path.write_bytes(str(number).encode())
            source_ids.append(
                str(
                    intake_source(
                        session,
                        IntakeRequest(
                            source_path=path,
                            source_root_id='root',
                            origin=Origin.MANUAL,
                            duration_seconds=180,
                            tag_observations=(
                                SourceTagObservation(format_name='flac', tag_name='ARTIST', value='Artist'),
                                SourceTagObservation(format_name='flac', tag_name='ALBUM', value='Album'),
                            ),
                            artwork_observations=(),
                            provider_attempts=(),
                            candidates=(),
                            review_decisions=(),
                        ),
                    ).source_id
                )
            )
        session.commit()

    release = Release.model_validate(
        {
            'id': '11111111-1111-4111-8111-111111111111',
            'title': 'Album',
            'media': [
                {
                    'position': 1,
                    'tracks': [
                        {
                            'id': '22222222-2222-4222-8222-222222222222',
                            'position': 1,
                            'title': 'First',
                            'recording': {'id': '33333333-3333-4333-8333-333333333333', 'title': 'First'},
                        },
                        {
                            'id': '44444444-4444-4444-8444-444444444444',
                            'position': 2,
                            'title': 'Second',
                            'recording': {'id': '33333333-3333-4333-8333-333333333333', 'title': 'Second'},
                        },
                    ],
                }
            ],
        }
    )
    with Session(engine) as session:
        context = load_context(session, selector)
        request = AlbumRemapApplyRequest(
            selector=selector,
            album_snapshot_token=context.album_snapshot_token,
            release_mbid=UUID(release.id),
            release_snapshot_token='0' * 64,
            assignments=(
                AlbumRemapAssignment(source_id=source_ids[0], track_mbid=UUID('22222222-2222-4222-8222-222222222222')),
                AlbumRemapAssignment(source_id=source_ids[1], track_mbid=UUID('44444444-4444-4444-8444-444444444444')),
            ),
            unmatched_source_ids=(),
        )

        # When: the full partition contains an invalid provider recording collision.
        with pytest.raises(AlbumRemapConflict, match='one recording identity'):
            _ = apply_remap(session, request, AlbumRemapRelease(release, '0' * 64))
        session.rollback()

    # Then: no source assignment or final revision from the rejected batch persists.
    with Session(engine) as session:
        assert session.query(SourceRecordingAssignmentRecord).count() == 0
        assert session.query(LibraryMetadataRevisionRecord).count() == 0


def test_album_remap_apply_when_metadata_write_fails_after_first_flush_rolls_back(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: one assignment whose metadata persistence fails after association state has flushed.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "album-remap-post-mutation-rollback.db"}')
    Base.metadata.create_all(engine)
    selector = AlbumRemapSelector(artist_name='Artist', album_name='Album')
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
        path = tmp_path / 'track.flac'
        path.write_bytes(b'track')
        source_id = intake_source(
            session,
            IntakeRequest(
                source_path=path,
                source_root_id='root',
                origin=Origin.MANUAL,
                duration_seconds=180,
                tag_observations=(
                    SourceTagObservation(format_name='flac', tag_name='ARTIST', value='Artist'),
                    SourceTagObservation(format_name='flac', tag_name='ALBUM', value='Album'),
                ),
                artwork_observations=(),
                provider_attempts=(),
                candidates=(),
                review_decisions=(),
            ),
        ).source_id
        session.commit()
    release = Release.model_validate(
        {
            'id': '11111111-1111-4111-8111-111111111111',
            'title': 'Album',
            'media': [
                {
                    'tracks': [
                        {
                            'id': '22222222-2222-4222-8222-222222222222',
                            'position': 1,
                            'title': 'Track',
                            'recording': {'id': '33333333-3333-4333-8333-333333333333', 'title': 'Track'},
                        }
                    ]
                }
            ],
        }
    )
    flushed_assignment = False

    def observe_flush(session: Session, _: object) -> None:
        nonlocal flushed_assignment
        flushed_assignment = flushed_assignment or any(
            isinstance(record, SourceRecordingAssignmentRecord) for record in session.new
        )

    def fail_metadata_write(*_: object) -> LibraryMetadataRevisionRecord:
        raise RuntimeError('forced metadata failure')

    with Session(engine) as session:
        context = load_context(session, selector)
        request = AlbumRemapApplyRequest(
            selector=selector,
            album_snapshot_token=context.album_snapshot_token,
            release_mbid=UUID(release.id),
            release_snapshot_token='0' * 64,
            assignments=(
                AlbumRemapAssignment(source_id=source_id, track_mbid=UUID('22222222-2222-4222-8222-222222222222')),
            ),
            unmatched_source_ids=(),
        )
        event.listen(session, 'after_flush', observe_flush)
        monkeypatch.setattr(album_remap, 'append_metadata_revision', fail_metadata_write)

        # When: persistence fails after the association mutation has flushed.
        with pytest.raises(RuntimeError, match='forced metadata failure'):
            _ = apply_remap(session, request, AlbumRemapRelease(release, '0' * 64))
        event.remove(session, 'after_flush', observe_flush)
        session.rollback()

    # Then: the transaction rollback removes every mutation from the failed remap.
    assert flushed_assignment
    with Session(engine) as session:
        assert session.query(SourceRecordingAssignmentRecord).count() == 0
        assert session.query(LibraryMetadataRevisionRecord).count() == 0
