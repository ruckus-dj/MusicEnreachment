from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest
from sqlalchemy.orm import Session

from music_ingest.models import LibraryPublicationRecord, SourceRecord
from music_ingest.models.entities import SourceRecordingAssignmentRecord
from music_ingest.services.publication import (
    PublicationAttemptRequest,
    expose_attempt,
    finalize_attempt,
    mark_staged,
    reserve_attempt,
)
from tests.adapters.test_lrclib_handler import (
    NOW,
    SYNCED_LYRICS,
    _engine,
    _enqueue,
    _events,
    _provider_response,
    _published_record,
    _run_worker,
)


@pytest.mark.parametrize(
    'change',
    [
        'none',
        'identical',
        'duration',
        'identity',
        'missing',
        'tampered',
        'unverified',
        'title',
        'settings',
        'disabled',
        'symlink',
    ],
)
def test_verified_recording_reuses_sidecar_across_mp3_flac(
    tmp_path: Path, change: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    engine = _engine(tmp_path)
    root = tmp_path / 'media'
    with Session(engine) as session:
        record, source, old = _published_record(session, root, audio_relative_path=Path('album/track.mp3'))
        record.musicbrainz_recording_id = '00000000-0000-4000-8000-000000000001'
        record.musicbrainz_release_id = '00000000-0000-4000-8000-000000000010'
        record.match_state = 'matched'
        session.add(
            SourceRecordingAssignmentRecord(
                source_id=source.id,
                library_record_id=record.id,
                state='automatic_verified',
                actor='provider',
                evidence_json='{}',
                created_at=NOW,
            )
        )
        _enqueue(session)
        session.commit()
        assert len(_run_worker(session, root, _provider_response()).calls) == 1
        path = Path(old.path) if change == 'identical' else root / 'other/track.flac'
        # A separately observed encoding, assigned to the same verified recording.
        replacement = SourceRecord(
            id='replacement-source',
            source_path=str(tmp_path / 'incoming/replacement.flac'),
            device=1,
            inode=2,
            size_bytes=2,
            sha256=source.sha256 if change == 'identical' else 'd' * 64,
            duration_seconds=source.duration_seconds,
            origin='manual',
            intake_state='stored',
            library_record_id=record.id,
        )
        session.add(replacement)
        if change != 'unverified':
            session.add(
                SourceRecordingAssignmentRecord(
                    source_id=replacement.id,
                    library_record_id=record.id,
                    state='automatic_verified',
                    actor='provider',
                    evidence_json='{}',
                    created_at=NOW,
                )
            )
        if change == 'duration':
            replacement.duration_seconds = 1
        if change == 'identity':
            record.musicbrainz_recording_id = '00000000-0000-4000-8000-000000000002'
        if change == 'settings':
            from music_ingest.adapters.external.lrclib import LrclibSettings, LrclibSettingsHolder

            monkeypatch.setattr(
                LrclibSettingsHolder, 'snapshot', lambda self: LrclibSettings(match_confidence_threshold=0.9)
            )
        if change == 'symlink':
            sidecar = Path(old.path).with_suffix('.lrc')
            saved = sidecar.with_suffix('.saved')
            sidecar.rename(saved)
            sidecar.symlink_to(saved)
        if change == 'missing':
            Path(old.path).with_suffix('.lrc').unlink()
        if change == 'tampered':
            Path(old.path).with_suffix('.lrc').write_text('[00:01]different\n[00:02]valid but tampered\n')
        if change == 'title':
            from music_ingest.models import LibraryMetadataRevisionRecord

            revision = session.get(LibraryMetadataRevisionRecord, old.metadata_revision_id)
            assert revision is not None
            revision.tags_json = revision.tags_json.replace('Fixture Track', 'Fixture Track (Live)')
        staging = tmp_path / 'staging/replacement'
        staging.mkdir(parents=True)
        (staging / path.name).write_bytes(b'new-output')
        evidence_before = record.lyrics_evidence_json
        attempt = reserve_attempt(
            session,
            PublicationAttemptRequest(
                'replacement',
                record.id,
                replacement.id,
                old.metadata_revision_id,
                path.parent,
                path.name,
                staging,
                tmp_path / 'backup',
                NOW,
            ),
        )
        assert attempt is not None
        mark_staged(session, attempt, NOW)
        expose_attempt(session, attempt, NOW)
        new = finalize_attempt(session, attempt, NOW, lrclib_enabled=change != 'disabled')
        assert record.lyrics_evidence_json == evidence_before
        if change == 'disabled':
            assert record.lyrics_status == 'none'
            assert record.lyrics_path is None
            assert Path(old.path).with_suffix('.lrc').read_text() == SYNCED_LYRICS
            session.commit()
            return
        assert record.lyrics_status == 'pending'
        assert record.lyrics_path is None
        session.commit()
        calls = _run_worker(session, root, _provider_response()).calls
        if change not in {'none', 'identical'}:
            assert len(calls) == 1
            return
        assert calls == []
        assert 'reused' in (_events(session)[-1].reason or '')
        assert source.sha256 == 'a' * 64
        assert record.lyrics_publication_id == new.id
        assert path.with_suffix('.lrc').read_text() == SYNCED_LYRICS
        assert Path(old.path).with_suffix('.lrc').read_text() == SYNCED_LYRICS


def test_explicit_retry_after_reused_negative_cache_calls_provider_before_expiry(tmp_path: Path) -> None:
    from music_ingest.services.lyrics.reuse import LyricEvidence
    from tests.adapters.test_lrclib_handler import _status_response

    engine = _engine(tmp_path)
    root = tmp_path / 'media'
    with Session(engine) as session:
        record, source, old = _published_record(session, root)
        _enqueue(session)
        session.commit()
        assert len(_run_worker(session, root, _status_response(404)).calls) == 1
        assert record.lyrics_evidence_json is not None
        original = LyricEvidence.model_validate_json(record.lyrics_evidence_json)
        old.state = 'superseded'
        session.add(
            LibraryPublicationRecord(
                id='replacement',
                library_record_id=record.id,
                source_id=source.id,
                path=old.path,
                format_name=old.format_name,
                content_sha256=old.content_sha256,
                metadata_revision_id=old.metadata_revision_id,
                state='current',
                created_at=NOW,
            )
        )
        _enqueue(session)
        session.commit()
        assert _run_worker(session, root, _status_response(404)).calls == []
        assert record.lyrics_evidence_json is not None
        cached = LyricEvidence.model_validate_json(record.lyrics_evidence_json)
        assert cached.expires_at is not None
        assert cached.expires_at > datetime.now(UTC)
        assert cached.model_dump(exclude={'publication_id'}) == original.model_dump(exclude={'publication_id'})

        _enqueue(session)
        session.commit()
        assert len(_run_worker(session, root, _provider_response()).calls) == 1
        assert record.lyrics_status == 'synced'
        assert cached.publication_id == 'replacement'


@pytest.mark.parametrize('status', [404, 503])
def test_negative_cache_expires_but_transient_errors_retry(tmp_path: Path, status: int) -> None:
    from music_ingest.services.lyrics.reuse import LyricEvidence
    from tests.adapters.test_lrclib_handler import _status_response

    engine = _engine(tmp_path)
    root = tmp_path / 'media'
    with Session(engine) as session:
        record, source, old = _published_record(session, root)
        _enqueue(session)
        session.commit()
        assert len(_run_worker(session, root, _status_response(status)).calls) == 1
        old.state = 'superseded'
        session.add(
            LibraryPublicationRecord(
                id='replacement',
                library_record_id=record.id,
                source_id=source.id,
                path=old.path,
                format_name=old.format_name,
                content_sha256=old.content_sha256,
                metadata_revision_id=old.metadata_revision_id,
                state='current',
                created_at=NOW,
            )
        )
        _enqueue(session)
        session.commit()
        calls = _run_worker(session, root, _status_response(status)).calls
        assert len(calls) == (0 if status == 404 else 1)
        if status == 404:
            assert record.lyrics_evidence_json is not None
            evidence = LyricEvidence.model_validate_json(record.lyrics_evidence_json)
            record.lyrics_evidence_json = evidence.model_copy(
                update={
                    'expires_at': datetime.now(UTC) - timedelta(seconds=1),
                }
            ).model_dump_json()
            _enqueue(session)
            session.commit()
            assert len(_run_worker(session, root, _status_response(status)).calls) == 1
