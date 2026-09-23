from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import (
    Base,
    FingerprintRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryRecord,
    PublicationAttemptRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
)
from music_ingest.repositories.jobs import JobRepository
from music_ingest.services.library.service import append_metadata_revision
from music_ingest.services.matching.providers import (
    FixtureProvenance,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    ReleaseCandidate,
)
from music_ingest.services.musicbrainz_identity import ConfirmedMusicBrainzIdentity
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.handlers import selection
from music_ingest.workers.worker import ProcessingWorker


def test_final_metadata_changed_compares_latest_source_revision() -> None:
    # Given: the latest Final revision already contains canonical provider metadata.
    now = datetime(2026, 9, 23, tzinfo=UTC)
    record = LibraryRecord(
        id='record-id',
        created_at=now,
        updated_at=now,
        metadata_revisions=[
            LibraryMetadataRevisionRecord(
                library_record_id='record-id',
                source_id='source-id',
                layer='final',
                revision=1,
                tags_json=json.dumps({'ARTIST': 'Canonical Artist'}, sort_keys=True),
                actor='folder_selection',
                created_at=now,
            )
        ],
    )

    # When: refreshed tags are equal or different.
    unchanged = selection.final_metadata_changed(record, 'source-id', {'ARTIST': 'Canonical Artist'})
    changed = selection.final_metadata_changed(record, 'source-id', {'ARTIST': 'Renamed Artist'})

    # Then: only a real tag difference requires a new revision and publication.
    assert not unchanged
    assert changed


def test_confirmed_pair_refresh_uses_exact_identity_without_candidate_selection(tmp_path: Path) -> None:
    # Given: a matched source has one confirmed pair and persisted fingerprint evidence.
    recording_mbid = 'aaaf4974-c2bd-41dc-9d10-b33f080957cd'
    release_mbid = 'fc71b952-9e68-4b22-bfdc-053233c2adbc'
    requests: list[MusicBrainzLookupRequest] = []
    fixture_path = tmp_path / 'musicbrainz.json'
    fixture_body = b'{}'
    _ = fixture_path.write_bytes(fixture_body)

    @dataclass(frozen=True, slots=True)
    class Provider:
        def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzMatch:
            _ = now
            requests.append(request)
            return MusicBrainzMatch(
                FixtureProvenance(fixture_path, sha256(fixture_body).hexdigest()),
                ReleaseCandidate(
                    release_mbid,
                    'Updated Album',
                    'Updated Artist',
                    recording_mbids=(recording_mbid,),
                    recording_title='Updated Track',
                    track_number=1,
                    track_total=1,
                    disc_number=1,
                    disc_total=1,
                    genres=('rock',),
                ),
            )

    source_path = tmp_path / 'source.flac'
    _ = source_path.write_bytes(b'audio')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "metadata-refresh-worker.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 23, tzinfo=UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path=str(tmp_path),
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(
            id='record',
            musicbrainz_recording_id=recording_mbid,
            musicbrainz_release_id=release_mbid,
            match_state='matched',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source',
            source_path=str(source_path),
            device=1,
            inode=1,
            size_bytes=source_path.stat().st_size,
            sha256='b' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            fingerprints=[
                FingerprintRecord(
                    state='success',
                    fingerprint='persisted-fingerprint',
                    duration_seconds=180,
                    tool_version='fixture',
                    output_sha256='c' * 64,
                )
            ],
        )
        session.add_all((root, record, source))
        _ = JobRepository(session).enqueue_musicbrainz_refresh(
            source.id,
            ConfirmedMusicBrainzIdentity(recording_mbid, release_mbid),
            now,
        )
        session.commit()

    # When: the dedicated refresh worker executes.
    with Session(engine) as session:
        worker = ProcessingWorker(
            session,
            ProcessingConfig(
                incoming_root=tmp_path,
                staging_root=tmp_path / 'staging',
                media_root=tmp_path / 'media',
                musicbrainz_provider=Provider(),
            ),
        )
        assert worker.run_once(allowed_kinds={'musicbrainz_refresh'})
        session.commit()

        # Then: MusicBrainz receives the exact pair and only publication selection follows.
        assert [(item.recording_mbid, item.release_mbid) for item in requests] == [(recording_mbid, release_mbid)]
        jobs = session.query(JobRecord).order_by(JobRecord.created_at, JobRecord.id).all()
        assert [(job.kind, job.state) for job in jobs] == [
            ('musicbrainz_refresh', 'completed'),
            ('selection_refresh', 'queued'),
        ]
        revisions = session.query(LibraryMetadataRevisionRecord).order_by(LibraryMetadataRevisionRecord.id).all()
        assert [(item.layer, item.actor) for item in revisions] == [
            ('analyzed', 'musicbrainz_refresh'),
            ('final', 'musicbrainz_refresh'),
        ]


@pytest.mark.parametrize(
    'identity_tags',
    [
        {'MUSICBRAINZ_RECORDINGID': 'recording-id'},
        {'MUSICBRAINZ_RECORDINGID': 'other-recording', 'MUSICBRAINZ_ALBUMID': 'other-release'},
    ],
)
def test_publication_rejects_incomplete_or_mismatched_musicbrainz_identity(
    tmp_path: Path, identity_tags: dict[str, str]
) -> None:
    # Given: final metadata does not preserve the record's complete confirmed identity.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "publication-identity.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 9, 23, tzinfo=UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path=str(tmp_path),
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(
            id='record',
            musicbrainz_recording_id='recording-id',
            musicbrainz_release_id='release-id',
            match_state='matched',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='source',
            source_path=str(tmp_path / 'source.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            review_decisions=[ReviewDecisionRecord(state='confirmed', rationale='fixture')],
        )
        session.add_all((root, record, source))
        session.flush()
        revision = append_metadata_revision(
            session,
            record.id,
            source.id,
            'final',
            {'TITLE': 'Track', **identity_tags},
            'test',
            now,
        )
        _ = JobRepository(session).enqueue(source.id, 'final_publish', now, revision.id)
        session.commit()

    # When: publication evaluates the final revision.
    with Session(engine) as session:
        assert ProcessingWorker(
            session,
            ProcessingConfig(tmp_path, tmp_path / 'staging', tmp_path / 'media'),
        ).run_once(allowed_kinds={'final_publish'})
        session.commit()

        # Then: the record is reviewable and no filesystem publication is reserved.
        refreshed = session.get(LibraryRecord, 'record')
        assert refreshed is not None and refreshed.processing_state == 'needs_review'
        event = session.query(LibraryEventRecord).filter_by(kind='publication_musicbrainz_identity_mismatch').one()
        assert event.state == 'needs_review'
        assert session.query(PublicationAttemptRecord).count() == 0
