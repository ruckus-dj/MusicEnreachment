import json
from datetime import UTC, datetime

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from music_ingest.dto.source_encoding import EncodingChoice, EncodingRequest
from music_ingest.library.service import _catalog_source_tags, append_metadata_revision, ensure_source_record
from music_ingest.models import Base, LibraryMetadataRevisionRecord, SourceRecord, SourceTagRecord
from music_ingest.source_encoding import apply_encoding


@pytest.fixture
def session():
    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        yield session


def make_source(session: Session, value: str) -> SourceRecord:
    source = SourceRecord(
        id='source',
        source_path='/absent',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        origin='manual',
        intake_state='present',
    )
    source.tag_observations.append(
        SourceTagRecord(format_name='mp3', tag_name='TITLE', value=value, prepared_bytes=value.encode('latin-1'))
    )
    session.add(source)
    session.flush()
    return source


def test_nonreversible_unicode_preview_and_apply_are_atomic(session: Session) -> None:
    from music_ingest.models import JobRecord
    from music_ingest.source_encoding import EncodingInvalid, preview_encoding

    source = make_source(session, 'þÿAB')
    second = SourceTagRecord(format_name='legacy', tag_name='ARTIST', value='Ïðèâåò')
    source.tag_observations.append(second)
    session.flush()
    first = source.tag_observations[0]
    request = EncodingRequest(
        expected_revision=1,
        choices=[
            EncodingChoice(field_id=second.id, mode='unicode', encode_codec='latin-1', decode_codec='cp1251'),
            EncodingChoice(field_id=first.id, mode='unicode', encode_codec='latin-1', decode_codec='utf-16'),
        ],
    )
    preview = preview_encoding(source, request)
    failed = next(item for item in preview.fields if item.field_id == first.id)
    assert failed.error == 'inverse_roundtrip_failed'
    assert failed.value is None
    with pytest.raises(EncodingInvalid):
        apply_encoding(session, source, request, datetime.now(UTC))
    session.flush()
    session.expire_all()
    assert first.value == 'þÿAB'
    assert second.value == 'Ïðèâåò'
    assert source.source_metadata_revision == 1
    assert first.applied_choice_json is None
    assert second.applied_choice_json is None
    assert not session.scalars(select(JobRecord)).all()
    assert not session.scalars(select(LibraryMetadataRevisionRecord)).all()


def test_apply_appends_current_original_preserving_import(session: Session) -> None:
    from music_ingest.api.catalog_views import _catalog_tags

    now = datetime.now(UTC)
    source = make_source(session, 'Ïðèâåò')
    record = ensure_source_record(session, source, now)
    first = append_metadata_revision(session, record.id, source.id, 'original', {'TITLE': 'Ïðèâåò'}, 'worker', now)
    assert (
        append_metadata_revision(session, record.id, source.id, 'original', {'TITLE': 'ignored'}, 'worker', now)
        is first
    )
    apply_encoding(
        session,
        source,
        EncodingRequest(
            expected_revision=1,
            choices=[EncodingChoice(field_id=source.tag_observations[0].id, mode='decode', decode_codec='cp1251')],
        ),
        now,
    )
    revisions = session.scalars(
        select(LibraryMetadataRevisionRecord)
        .where(LibraryMetadataRevisionRecord.layer == 'original')
        .order_by(LibraryMetadataRevisionRecord.revision)
    ).all()
    assert [json.loads(item.tags_json)['TITLE'] for item in revisions] == ['Ïðèâåò', 'Привет']
    session.expire(record)
    assert _catalog_tags(record, source.id)['TITLE'] == 'Привет'
    assert [item.tags for item in _catalog_source_tags(session, None)] == [{'TITLE': 'Привет'}]

    # Latest Final wins even when Original has a higher revision number.
    append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Old Final'}, 'manual', now)
    append_metadata_revision(session, record.id, source.id, 'final', {'TITLE': 'Current Final'}, 'manual', now)
    session.add(
        LibraryMetadataRevisionRecord(
            library_record_id=record.id,
            source_id=source.id,
            layer='original',
            revision=3,
            tags_json=json.dumps({'TITLE': 'Newer Original'}),
            actor='source_encoding',
            created_at=now,
        )
    )
    session.flush()
    assert [item.tags for item in _catalog_source_tags(session, None)] == [{'TITLE': 'Current Final'}]


@pytest.mark.parametrize('value', ['ASCII', ''])
def test_decision_only_apply_versions_without_stranding_jobs(session: Session, value: str) -> None:
    from music_ingest.models import JobRecord, ProviderCandidateRunRecord
    from music_ingest.models.jobs import JobRepository

    now = datetime.now(UTC)
    source = make_source(session, value)
    job = JobRepository(session).enqueue(source.id, 'musicbrainz_analysis', now)
    run = ProviderCandidateRunRecord(provider_name='musicbrainz', source_metadata_revision=1, created_at=now)
    source.candidate_runs.append(run)
    session.flush()
    choice = EncodingChoice(field_id=source.tag_observations[0].id, mode='decode', decode_codec='cp1251')
    result = apply_encoding(session, source, EncodingRequest(expected_revision=1, choices=[choice]), now)
    assert result.source_revision == 2
    assert not result.queued
    assert source.tag_observations[0].applied_choice_json == choice.model_dump_json()
    assert job is not None and job.state == 'queued' and job.source_metadata_revision == 2
    assert run.source_metadata_revision == 2
    repeat = apply_encoding(session, source, EncodingRequest(expected_revision=2, choices=[choice]), now)
    assert repeat.source_revision == 2
    assert len(session.scalars(select(JobRecord)).all()) == 1


def test_id3_physical_precedence_and_numeric_genre_keep_evidence() -> None:
    from music_ingest.normalize.source_evidence import _id3_fields
    from music_ingest.normalize.source_values import source_values

    def frame(name: bytes, value: bytes) -> bytes:
        payload = b'\x00' + value
        return name + len(payload).to_bytes(4, 'big') + b'\0\0' + payload

    fields = _id3_fields(frame(b'TYER', b'1999') + frame(b'TDRC', b'2000') + frame(b'TCON', b'(17)'), 3, 0)
    assert source_values(fields) == {'DATE': '2000', 'GENRE': 'Rock'}
    assert [(field.value, field.selected) for field in fields] == [('1999', False), ('2000', True), ('(17)', True)]
    assert fields[-1].prepared_bytes == b'(17)'


def test_stage_plan_uses_persisted_projection_without_reread() -> None:
    from pathlib import Path

    from music_ingest.processing.media_stage import plan_media_stage

    tags = (('TITLE', 'Current'), ('GENRE', 'Rock'), ('DATE', '2000'))
    plan = plan_media_stage(Path('/absent.mp3'), source_tags=tags)
    assert plan.source_tags == tags
