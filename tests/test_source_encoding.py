from datetime import UTC, datetime
from pathlib import Path

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.enrichment.fingerprints import FingerprintResult, FingerprintState
from music_ingest.models import Base, JobAttemptRecord, JobRecord, SourceRecord, SourceTagRecord
from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing import ProcessingConfig
from music_ingest.processing.execution import ExecutionContext
from music_ingest.processing.handlers.analysis import AnalysisHandler
from music_ingest.processing.support.evidence import SourceEvidence
from music_ingest.processing.support.settings import RuntimeProcessingSettings
from music_ingest.processing.support.sources import SourceAccess


def test_analysis_missing_fingerprint_never_reads_source(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        config = ProcessingConfig(tmp_path, tmp_path / 'stage', tmp_path / 'out')
        evidence = SourceEvidence(session, config, RuntimeProcessingSettings(session, config))
        source = SourceRecord(id='missing')

        def forbidden(*args: object, **kwargs: object) -> None:
            pytest.fail('provider processing attempted fingerprint calculation')

        monkeypatch.setattr('music_ingest.processing.support.evidence.fingerprint_source', forbidden)
        assert evidence.analyze_source(source, tmp_path / 'does-not-exist') is None


def test_musicbrainz_uses_db_tags_without_source_read(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        source = SourceRecord(
            id='source',
            source_path=str(tmp_path / 'absent'),
            source_root_id='root',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
        )
        source.tag_observations.append(SourceTagRecord(format_name='legacy', tag_name='TITLE', value='Database title'))
        source.tag_observations.append(
            SourceTagRecord(format_name='mp3', tag_name='TITLE', value='Stale ID3v1', selected=False)
        )
        session.add(source)
        session.flush()
        monkeypatch.setattr(SourceAccess, 'locked_source', lambda *args: source)
        monkeypatch.setattr(SourceAccess, 'owned_source_path', lambda *args: tmp_path / 'absent')
        monkeypatch.setattr(SourceAccess, 'changed', lambda *args: False)
        monkeypatch.setattr(
            SourceEvidence,
            'analyze_source',
            lambda *args: FingerprintResult(FingerprintState.SUCCESS, 'stored', 60.0, None, 'a' * 64, None, None),
        )
        observed = []

        def lookup(self, tags, fingerprint, now, **kwargs):
            observed.append((tags, kwargs))
            return None

        monkeypatch.setattr(AnalysisHandler, 'lookup_providers', lookup)
        config = ProcessingConfig(tmp_path, tmp_path / 'stage', tmp_path / 'out')
        settings = RuntimeProcessingSettings(session, config)
        handler = AnalysisHandler(session, SourceAccess(session), SourceEvidence(session, config, settings), settings)
        job = JobRecord(id='job', source_id=source.id, kind='musicbrainz_analysis', state='running', created_at=now)
        handler.handle(ClaimedJob(job, JobAttemptRecord()), ExecutionContext(session, config, now, settings))
        assert observed[0][0] == (('TITLE', 'Database title'),)
        assert observed[0][1]['run_acoustid'] is False


def test_preview_strict_field_recovery_and_legacy_evidence() -> None:
    from music_ingest.dto.source_encoding import EncodingChoice
    from music_ingest.source_encoding import preview_field

    field = SourceTagRecord(id=1, format_name='id3v1', tag_name='TITLE', value='Ïðèâåò')
    choice = EncodingChoice(field_id=1, mode='unicode', encode_codec='latin-1', decode_codec='cp1251')
    assert preview_field(field, choice).value == 'Привет'
    assert (
        preview_field(field, EncodingChoice(field_id=1, mode='decode', decode_codec='cp1251')).error
        == 'raw_evidence_unavailable'
    )
    field.value = 'Beyoncé'
    assert preview_field(field, EncodingChoice(field_id=1, mode='keep')).value == 'Beyoncé'
    field.value = 'Привет'
    assert preview_field(field, choice).error == 'strict_conversion_failed'


@pytest.mark.parametrize('pending_state', ['reserved', 'staged', 'prepared', 'exposed'])
def test_apply_is_atomic_revision_guarded_and_queues_only_musicbrainz(pending_state: str) -> None:
    from music_ingest.dto.source_encoding import EncodingChoice, EncodingRequest
    from music_ingest.source_encoding import EncodingConflict, EncodingInvalid, apply_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = SourceRecord(
            id='source',
            source_path='/absent',
            source_root_id='root',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
        )
        source.tag_observations.extend(
            [
                SourceTagRecord(format_name='legacy', tag_name='TITLE', value='Ïðèâåò'),
                SourceTagRecord(format_name='legacy', tag_name='ARTIST', value='Beyoncé'),
            ]
        )
        session.add(source)
        session.flush()
        first, second = source.tag_observations
        good = EncodingChoice(field_id=first.id, mode='unicode', encode_codec='latin-1', decode_codec='cp1251')
        bad = EncodingChoice(field_id=second.id, mode='decode', decode_codec='cp1251')
        with pytest.raises(EncodingInvalid):
            apply_encoding(
                session, source, EncodingRequest(expected_revision=1, choices=[good, bad]), datetime.now(UTC)
            )
        assert first.value == 'Ïðèâåò'
        result = apply_encoding(
            session, source, EncodingRequest(expected_revision=1, choices=[good]), datetime.now(UTC)
        )
        assert result.source_revision == 2
        assert first.value == 'Привет'
        assert first.original_value == 'Ïðèâåò'
        from sqlalchemy import select

        jobs = session.scalars(select(JobRecord)).all()
        assert [job.kind for job in jobs] == ['musicbrainz_analysis']
        assert jobs[0].source_metadata_revision == 2
        from music_ingest.models import PublicationAttemptRecord

        pending = PublicationAttemptRecord(
            id='attempt',
            library_record_id=source.library_record_id,
            source_id=source.id,
            state=pending_state,
            target_directory='/managed',
            target_audio_name='audio.mka',
            staging_directory='/stage',
            backup_directory='/backup',
            created_at=datetime.now(UTC),
        )
        session.add(pending)
        session.flush()
        with pytest.raises(EncodingConflict, match='source_processing_busy'):
            apply_encoding(
                session,
                source,
                EncodingRequest(expected_revision=2, choices=[EncodingChoice(field_id=first.id, mode='original')]),
                datetime.now(UTC),
            )
        with pytest.raises(EncodingConflict):
            apply_encoding(session, source, EncodingRequest(expected_revision=1, choices=[good]), datetime.now(UTC))


def test_apply_preserves_queued_folder_selection_for_other_folder_members() -> None:
    from sqlalchemy import select

    from music_ingest.dto.source_encoding import EncodingChoice, EncodingRequest
    from music_ingest.models.jobs import JobRepository
    from music_ingest.source_encoding import apply_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        source = SourceRecord(
            id='source',
            source_path='/album/one.mp3',
            source_root_id='root',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
        )
        other = SourceRecord(
            id='other',
            source_path='/album/two.mp3',
            source_root_id='root',
            device=1,
            inode=2,
            size_bytes=1,
            sha256='b' * 64,
            origin='manual',
            intake_state='present',
        )
        source.tag_observations.append(SourceTagRecord(format_name='legacy', tag_name='TITLE', value='Ïðèâåò'))
        session.add_all([source, other])
        session.flush()
        folder_job = JobRepository(session).enqueue_folder_release_selection('/album', now)
        choice = EncodingChoice(
            field_id=source.tag_observations[0].id,
            mode='unicode',
            encode_codec='latin-1',
            decode_codec='cp1251',
        )

        apply_encoding(session, source, EncodingRequest(expected_revision=1, choices=[choice]), now)

        assert folder_job is not None and folder_job.state == 'queued'
        assert [job.kind for job in session.scalars(select(JobRecord)).all()] == [
            'folder_release_selection',
            'musicbrainz_analysis',
        ]
