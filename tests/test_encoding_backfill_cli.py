from pathlib import Path

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.models import Base, SourceRecord, SourceTagRecord


def test_cli_requires_explicit_scope_and_defaults_to_dry_run() -> None:
    from music_ingest.cli.encoding_backfill import parser

    with pytest.raises(SystemExit):
        parser().parse_args(['--report', 'report.jsonl'])
    args = parser().parse_args(['--root-id', 'root', '--report', 'report.jsonl'])
    assert args.apply is False
    with pytest.raises(SystemExit):
        parser().parse_args(['--root-id', 'root', '--report', 'report.jsonl', '--limit', '0'])


def test_scoped_report_dry_run_leaves_database_unchanged(tmp_path: Path) -> None:
    from music_ingest.cli.encoding_backfill import run_backfill

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        for number in range(2):
            item = SourceRecord(
                id=str(number),
                source_root_id='root',
                source_path=f'/absent/{number}',
                device=1,
                inode=number,
                size_bytes=1,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
            )
            item.tag_observations.append(
                SourceTagRecord(
                    format_name='ID3v2',
                    tag_name='TITLE',
                    value='Ìîðÿ÷åê',
                    original_value='Ìîðÿ÷åê',
                    extraction_version='test',
                    selected=True,
                )
            )
            session.add(item)
        session.commit()
    path = tmp_path / 'report.jsonl'
    summary = run_backfill(engine, path, root_id='root', limit=1)
    assert summary['counts'] == {'would_change': 1}
    assert summary['has_more'] is True
    assert path.read_text().count('\n') == 2
    with Session(engine) as session:
        item = session.get(SourceRecord, '0')
        assert item is not None
        assert item.source_metadata_revision == 1
        assert item.tag_observations[0].value == 'Ìîðÿ÷åê'
    with pytest.raises(FileExistsError):
        run_backfill(engine, path, root_id='root')


def test_cli_apply_idempotent_manual_safe_and_paginates(tmp_path: Path) -> None:
    from datetime import UTC, datetime

    from sqlalchemy import select

    from music_ingest.cli.encoding_backfill import run_backfill
    from music_ingest.dto.source_encoding import EncodingChoice, EncodingRequest
    from music_ingest.models import JobRecord
    from music_ingest.source_encoding import apply_encoding

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        for number in range(3):
            item = SourceRecord(
                id=str(number),
                source_root_id='root' if number < 2 else 'other',
                source_path=f'/nonexistent/{number}/audio',
                device=1,
                inode=number,
                size_bytes=1,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
            )
            item.tag_observations.append(
                SourceTagRecord(
                    format_name='ID3v2',
                    tag_name='TITLE',
                    value='Ìîðÿ÷åê',
                    original_value='Ìîðÿ÷åê',
                    extraction_version='test',
                    selected=True,
                )
            )
            session.add(item)
        session.commit()
    first = run_backfill(engine, tmp_path / 'first.jsonl', root_id='root', limit=1, apply=True)
    assert first['counts'] == {'changed': 1}
    assert first['next_after_id'] == '0'
    second = run_backfill(engine, tmp_path / 'second.jsonl', root_id='root', after_id='0', apply=True)
    assert second['counts'] == {'changed': 1}
    assert second['has_more'] is False
    repeat = run_backfill(engine, tmp_path / 'repeat.jsonl', root_id='root', apply=True)
    assert repeat['counts'] == {'unchanged': 2}
    with Session(engine) as session:
        assert len(list(session.scalars(select(JobRecord)))) == 2
        item = session.get(SourceRecord, '0')
        assert item is not None
        field = item.tag_observations[0]
        apply_encoding(
            session,
            item,
            EncodingRequest(expected_revision=2, choices=[EncodingChoice(field_id=field.id, mode='original')]),
            datetime.now(UTC),
        )
        session.commit()
    manual = run_backfill(engine, tmp_path / 'manual.jsonl', source_ids=['0'], apply=True)
    assert manual['counts'] == {'unchanged': 1}
    with Session(engine) as session:
        item = session.get(SourceRecord, '0')
        other = session.get(SourceRecord, '2')
        assert item is not None and other is not None
        assert item.tag_observations[0].value == 'Ìîðÿ÷åê'
        assert item.tag_observations[0].decision_origin == 'manual'
        assert other.source_metadata_revision == 1
        assert other.tag_observations[0].value == 'Ìîðÿ÷åê'


def test_cli_reports_busy_skip_and_bounded_retries(tmp_path: Path) -> None:
    from datetime import UTC, datetime

    from music_ingest.cli.encoding_backfill import run_backfill
    from music_ingest.models import JobRecord

    engine = create_engine('sqlite://')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(
            SourceRecord(
                id='busy',
                source_root_id='root',
                source_path='/absent/audio',
                device=1,
                inode=1,
                size_bytes=1,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
            )
        )
        session.add(
            JobRecord(
                id='running',
                source_id='busy',
                kind='musicbrainz_analysis',
                state='running',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()
    summary = run_backfill(engine, tmp_path / 'busy.jsonl', source_ids=['busy'], apply=True, busy_retries=2)
    assert summary['counts'] == {'busy': 1}
    assert summary['busy_retries'] == 2
    assert summary['retry_source_ids'] == ['busy']
