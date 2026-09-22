"""Explicit DB-only encoding backfill. Does not migrate, scan, or start workers."""

import argparse
import json
import os
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import Engine, create_engine, select, text
from sqlalchemy.orm import Session

from music_ingest.models import SourceRecord
from music_ingest.services.source_encoding import backfill_source_encoding


def positive(value: str) -> int:
    number = int(value)
    if number < 1:
        raise argparse.ArgumentTypeError('must be positive')
    return number


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    scope = result.add_mutually_exclusive_group(required=True)
    scope.add_argument('--root-id')
    scope.add_argument('--source-id', action='append')
    result.add_argument('--limit', type=positive, default=100)
    result.add_argument('--after-id', help='resume after the previous report next_after_id')
    result.add_argument('--busy-retries', type=int, choices=range(4), default=0)
    mode = result.add_mutually_exclusive_group()
    mode.add_argument('--apply', action='store_true', help='commit changes; default is dry-run')
    mode.add_argument('--dry-run', action='store_true', help='explicitly select the safe default')
    result.add_argument('--report', type=Path, required=True, help='new JSONL report (never overwritten)')
    return result


def run_backfill(
    engine: Engine,
    report: Path,
    *,
    root_id: str | None = None,
    source_ids: list[str] | None = None,
    limit: int = 100,
    apply: bool = False,
    after_id: str | None = None,
    busy_retries: int = 0,
) -> dict[str, object]:
    if bool(root_id) == bool(source_ids) or limit < 1 or busy_retries not in range(4):
        raise ValueError('provide exactly one nonempty scope, a positive limit and 0..3 busy retries')
    query = select(SourceRecord.id).order_by(SourceRecord.id).limit(limit + 1)
    query = (
        query.where(SourceRecord.source_root_id == root_id)
        if root_id
        else query.where(SourceRecord.id.in_(source_ids or []))
    )
    if after_id is not None:
        query = query.where(SourceRecord.id > after_id)
    counts: Counter[str] = Counter()
    retry_ids: list[str] = []
    retries = 0
    # Exclusive creation fails before any mutation; interrupted runs retain source receipts.
    with report.open('x', encoding='utf-8') as output:
        with Session(engine) as session:
            ids = list(session.scalars(query))
        for source_id in ids[:limit]:
            row: dict[str, object] = {}
            for attempt in range(busy_retries + 1):
                with Session(engine) as session:
                    if engine.dialect.name == 'postgresql':
                        session.execute(text("SET LOCAL statement_timeout = '10s'"))
                        session.execute(text("SET LOCAL lock_timeout = '1s'"))
                    source = session.get(SourceRecord, source_id)
                    if source is None:
                        row = {'source_id': source_id, 'status': 'missing'}
                    else:
                        row = backfill_source_encoding(session, source, datetime.now(UTC), apply=apply)
                    if apply:
                        session.commit()
                    else:
                        session.rollback()
                if row['status'] != 'busy' or attempt == busy_retries:
                    break
                retries += 1
            if row['status'] == 'busy':
                retry_ids.append(source_id)
            counts[str(row['status'])] += 1
            output.write(json.dumps(row, ensure_ascii=False) + '\n')
            output.flush()
            os.fsync(output.fileno())
        summary: dict[str, object] = {
            'type': 'summary',
            'apply': apply,
            'root_id': root_id,
            'source_ids': source_ids,
            'limit': limit,
            'processed': sum(counts.values()),
            'counts': dict(counts),
            'has_more': len(ids) > limit,
            'next_after_id': ids[min(limit, len(ids)) - 1] if ids else after_id,
            'busy_retries': retries,
            'retry_source_ids': retry_ids,
        }
        output.write(json.dumps(summary, ensure_ascii=False) + '\n')
        output.flush()
        os.fsync(output.fileno())
    return summary


def main(arguments: list[str]) -> None:
    from music_ingest.bootstrap.server import RuntimeConfig

    args = parser().parse_args(arguments)
    config = RuntimeConfig.from_environment(os.environ)
    engine = create_engine(config.database_url, connect_args={'connect_timeout': config.connect_timeout_seconds})
    try:
        summary = run_backfill(
            engine,
            args.report,
            root_id=args.root_id,
            source_ids=args.source_id,
            limit=args.limit,
            apply=args.apply,
            after_id=args.after_id,
            busy_retries=args.busy_retries,
        )
        print(json.dumps(summary, ensure_ascii=False))
    finally:
        engine.dispose()
