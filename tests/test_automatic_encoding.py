from datetime import UTC, datetime

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.contracts.source_encoding import EncodingChoice, EncodingRequest
from music_ingest.models import Base, JobRecord, SourceRecord, SourceTagRecord
from music_ingest.services.source_encoding import apply_encoding, preview_field


def source(session: Session, values: tuple[str, ...] = ('Морячек', 'Ангус', 'Фиги')) -> SourceRecord:
    item = SourceRecord(
        id='auto',
        source_path='/absent/audio.mp3',
        source_root_id='root',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        origin='manual',
        intake_state='present',
    )
    for name, value in zip(('TITLE', 'ARTIST', 'ALBUM'), values, strict=False):
        damaged = value.encode('cp1251').decode('latin-1')
        item.tag_observations.append(
            SourceTagRecord(
                format_name='ID3v2',
                tag_name=name,
                value=damaged,
                original_value=damaged,
                declared_codec='utf-8',
                extraction_version='test',
                prepared_bytes=damaged.encode(),
                selected=True,
            )
        )
    session.add(item)
    session.flush()
    return item


def test_backfill_is_db_only_dry_run_default_apply_idempotent_and_manual_safe() -> None:
    from music_ingest.services.source_encoding import backfill_source_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session)
        now = datetime.now(UTC)
        assert backfill_source_encoding(session, item, now)['status'] == 'would_change'
        assert item.source_metadata_revision == 1
        assert item.tag_observations[0].value != 'Морячек'
        assert backfill_source_encoding(session, item, now, apply=True)['status'] == 'changed'
        assert all(field.decision_origin == 'auto' for field in item.tag_observations)
        assert [job.kind for job in session.scalars(select(JobRecord))] == ['musicbrainz_analysis']
        revision = item.source_metadata_revision
        assert backfill_source_encoding(session, item, now, apply=True)['status'] == 'unchanged'
        assert item.source_metadata_revision == revision
        field = item.tag_observations[0]
        apply_encoding(
            session,
            item,
            EncodingRequest(expected_revision=revision, choices=[EncodingChoice(field_id=field.id, mode='original')]),
            now,
        )
        assert backfill_source_encoding(session, item, now, apply=True)['manual_fields'] == 1
        assert field.value == field.original_value


@pytest.mark.parametrize('state', ['running', 'legacy'])
def test_backfill_skips_busy_and_missing_evidence(state: str) -> None:
    from music_ingest.services.source_encoding import backfill_source_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session)
        now = datetime.now(UTC)
        if state == 'running':
            session.add(
                JobRecord(id='running', source_id=item.id, kind='musicbrainz_analysis', state='running', created_at=now)
            )
        else:
            for field in item.tag_observations:
                field.extraction_version = None
        session.flush()
        assert backfill_source_encoding(session, item, now, apply=True)['status'] == (
            'busy' if state == 'running' else 'legacy_missing'
        )
        assert item.source_metadata_revision == 1


def test_initial_automatic_recovery_preserves_original_and_has_no_jobs() -> None:
    from music_ingest.services.source_encoding import recover_import_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session)
        originals = [f.original_value for f in item.tag_observations]
        assert recover_import_encoding(session, item, datetime.now(UTC)) == 3
        assert [f.value for f in item.tag_observations] == ['Морячек', 'Ангус', 'Фиги']
        assert [f.original_value for f in item.tag_observations] == originals
        assert all(f.decision_origin == 'auto' for f in item.tag_observations)
        assert session.scalars(select(JobRecord)).all() == []
        assert recover_import_encoding(session, item, datetime.now(UTC)) == 0


def test_codec_selection_is_from_immutable_original_after_auto_and_manual() -> None:
    from music_ingest.services.source_encoding import recover_import_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session)
        now = datetime.now(UTC)
        recover_import_encoding(session, item, now)
        field = item.tag_observations[0]
        choice = EncodingChoice(field_id=field.id, mode='codec', decode_codec='cp1251')
        assert preview_field(field, choice).value == 'Морячек'
        apply_encoding(
            session,
            item,
            EncodingRequest(
                expected_revision=item.source_metadata_revision,
                choices=[EncodingChoice(field_id=field.id, mode='original')],
            ),
            now,
        )
        assert field.value == field.original_value
        assert field.decision_origin == 'manual'
        assert recover_import_encoding(session, item, now) == 0
        assert preview_field(field, choice).value == 'Морячек'
        assert preview_field(
            field, EncodingChoice(field_id=field.id, mode='codec', decode_codec='cp866')
        ).value == field.original_value.encode('latin-1').decode('cp866')


@pytest.mark.parametrize('original', ['BeyoncÃ©', 'BeyoncÃƒÂ©'])
def test_same_declared_codec_recovers_restores_and_repeats_after_apply(original: str) -> None:
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session, ('Морячек',))
        field = item.tag_observations[0]
        field.value = original
        field.original_value = original
        evidence = original.encode('utf-8')
        field.prepared_bytes = evidence
        session.flush()
        recovered = original.encode('cp1252').decode('utf-8')
        choice = EncodingChoice(field_id=field.id, mode='codec', decode_codec='utf-8')
        restore = EncodingChoice(field_id=field.id, mode='original')
        assert preview_field(field, restore).value == original
        assert preview_field(field, choice).value == recovered
        now = datetime.now(UTC)
        applied = apply_encoding(session, item, EncodingRequest(expected_revision=1, choices=[choice]), now)
        assert applied.source_revision == 2
        session.expire_all()
        assert field.value == recovered
        assert preview_field(field, choice).status == 'unchanged'
        repeat = apply_encoding(session, item, EncodingRequest(expected_revision=2, choices=[choice]), now)
        assert repeat.source_revision == 2
        assert not repeat.queued
        assert field.value == recovered
        assert len(session.scalars(select(JobRecord)).all()) == 1
        assert preview_field(field, restore).value == original
        restored = apply_encoding(session, item, EncodingRequest(expected_revision=2, choices=[restore]), now)
        assert restored.source_revision == 3
        session.expire_all()
        assert field.value == original
        assert field.original_value == original
        assert field.prepared_bytes == evidence
        assert preview_field(field, choice).value == recovered


@pytest.mark.parametrize('value', ['Beyoncé', 'Motörhead', 'Île', 'Don’t', 'Привет', 'ASCII', ''])
def test_same_declared_codec_preserves_correct_unicode_or_reports_unavailable(value: str) -> None:
    field = SourceTagRecord(
        id=1, value=value, original_value=value, declared_codec='utf-8', prepared_bytes=value.encode('utf-8')
    )
    result = preview_field(field, EncodingChoice(field_id=1, mode='codec', decode_codec='utf-8'))
    if result.error is not None:
        assert result.error == 'strict_conversion_failed'
        assert result.value is None
    else:
        assert result.status == 'unchanged'
        assert result.value == value
    assert field.value == value
    assert field.original_value == value
    assert field.prepared_bytes == value.encode('utf-8')


@pytest.mark.parametrize('declared_codec', ['latin-1', 'utf-8'])
def test_codec_selection_rejects_nonreversible_utf16(declared_codec: str) -> None:
    field = SourceTagRecord(
        id=1,
        value='þÿAB',
        original_value='þÿAB',
        declared_codec=declared_codec,
        prepared_bytes='þÿAB'.encode(declared_codec),
    )
    result = preview_field(field, EncodingChoice(field_id=1, mode='codec', decode_codec='utf-16'))
    assert result.error == 'strict_conversion_failed'
    assert result.value is None
    assert field.value == 'þÿAB'


@pytest.mark.parametrize('value', ['Beyoncé', 'Motörhead', 'Île', 'Don’t', 'Привет', '', '    ', 'Ôèãè'])
def test_auto_does_not_change_controls(value: str) -> None:
    from music_ingest.services.source_encoding import recover_import_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        item = source(session, ('Морячек',))
        item.tag_observations[0].value = value
        item.tag_observations[0].original_value = value
        assert recover_import_encoding(session, item, datetime.now(UTC)) == 0
        assert item.tag_observations[0].value == value
