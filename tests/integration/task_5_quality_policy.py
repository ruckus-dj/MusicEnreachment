from __future__ import annotations

import json
import sys
from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.library.service import persist_effective_source_decision
from music_ingest.models import Base, EffectiveSourceDecisionRecord, LibraryRecord, SourceRecord
from music_ingest.quality_policy import Codec, QualityCandidate, quality_tuple


def main(output_path: Path) -> None:
    now = datetime(2026, 8, 11, tzinfo=UTC)
    candidates = (
        QualityCandidate('flac-24-96', Codec.FLAC, 24, 96_000, 2, None, True, 'present', False),
        QualityCandidate('flac-16-44', Codec.FLAC, 16, 44_100, 2, None, True, 'present', False),
        QualityCandidate('alac-24-96', Codec.ALAC, 24, 96_000, 2, None, True, 'present', False),
        QualityCandidate('opus-320', Codec.OPUS, None, 44_100, 2, 320_000, True, 'present', False),
        QualityCandidate('mp3-320', Codec.MP3, None, 44_100, 2, 320_000, True, 'present', False),
    )
    engine = create_engine('sqlite+pysqlite:///:memory:')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        record = LibraryRecord(id='record-task-5', created_at=now, updated_at=now)
        session.add(record)
        session.add_all(
            SourceRecord(
                id=candidate.source_id,
                source_path=f'/fixture/{candidate.source_id}',
                device=1,
                inode=index,
                size_bytes=1,
                sha256=f'{index:064x}',
                duration_seconds=1,
                origin='manual',
                intake_state='present',
                library_record=record,
            )
            for index, candidate in enumerate(candidates, start=1)
        )
        session.commit()
        decision = persist_effective_source_decision(session, record.id, candidates, now)
        session.commit()
        stored = session.get(EffectiveSourceDecisionRecord, record.id)
        assert stored is not None
        report = {
            'fixtures': [{'id': candidate.source_id, 'tuple': quality_tuple(candidate)} for candidate in candidates],
            'stored': {
                'baseline_source_id': stored.baseline_source_id,
                'policy_version': stored.policy_version,
                'quality_tuple_json': stored.quality_tuple_json,
                'reason': json.loads(stored.reason),
                'source_id': stored.source_id,
            },
            'winner': {'reason': decision.reason.value, 'source_id': decision.source_id},
        }
    engine.dispose()
    _ = output_path.write_text(json.dumps(report, indent=2, sort_keys=True) + '\n', encoding='utf-8')


if __name__ == '__main__':
    main(Path(sys.argv[1]))
