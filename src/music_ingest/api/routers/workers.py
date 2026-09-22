from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path
from typing import cast

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from sqlalchemy import case, func, select

from music_ingest.api.dependencies import SessionFactory
from music_ingest.models import JobRecord, SourceRecord
from music_ingest.services.normalize.source_values import source_values
from music_ingest.services.settings import build_runtime_settings
from music_ingest.workers.runtime import ProcessingRuntimeMonitor

_WORKER_QUEUE_JOB_LIMIT = 100


def create_router(
    session_factory: SessionFactory,
    *,
    worker_monitor: ProcessingRuntimeMonitor | None = None,
) -> APIRouter:
    router = APIRouter()

    @router.get('/api/workers/queue')
    def worker_queue() -> JSONResponse:
        with session_factory() as session:
            observed_at = datetime.now(UTC)
            slots = () if worker_monitor is None else worker_monitor.snapshots()
            active_job_ids = {slot.job_id for slot in slots if slot.state == 'processing' and slot.job_id is not None}
            counts = cast(
                tuple[int, int, int],
                cast(
                    object,
                    session.execute(
                        select(
                            func.count(JobRecord.id),
                            func.coalesce(
                                func.sum(
                                    case(
                                        (
                                            (JobRecord.state == 'queued')
                                            & (JobRecord.next_attempt_at.is_not(None))
                                            & (JobRecord.next_attempt_at > observed_at),
                                            1,
                                        ),
                                        else_=0,
                                    )
                                ),
                                0,
                            ),
                            func.coalesce(
                                func.sum(case((JobRecord.state == 'running', 1), else_=0)),
                                0,
                            ),
                        ).where(JobRecord.state.in_(['queued', 'running']))
                    ).one(),
                ),
            )
            total_active = int(counts[0])
            retry_wait_count = int(counts[1])
            durable_running_count = int(counts[2])
            jobs = list(
                session.scalars(
                    select(JobRecord)
                    .where(JobRecord.state.in_(['queued', 'running']))
                    .order_by(JobRecord.id.in_(active_job_ids).desc(), JobRecord.created_at, JobRecord.id)
                    .limit(_WORKER_QUEUE_JOB_LIMIT)
                ).all()
            )
            source_ids = {job.source_id for job in jobs if job.source_id is not None}
            sources = {
                source.id: source
                for source in session.scalars(select(SourceRecord).where(SourceRecord.id.in_(source_ids))).all()
            }

            def source_target(job: JobRecord) -> dict[str, str] | None:
                if job.source_id is None:
                    return None
                source = sources.get(job.source_id)
                if source is None or source.library_record_id is None:
                    return None
                tags = source_values(source.tag_observations)
                return {
                    'record_id': source.library_record_id,
                    'source_id': source.id,
                    'title': tags.get('TITLE', Path(source.source_path).stem),
                    'artist': tags.get('ARTIST', ''),
                    'album': tags.get('ALBUM', ''),
                    'path': source.source_path,
                }

            return JSONResponse(
                content={
                    'observed_at': observed_at.isoformat(),
                    'worker': {
                        'configured_concurrency': sum(
                            build_runtime_settings(session).worker_pools.model_dump().values()
                        ),
                        'pools': build_runtime_settings(session).worker_pools.model_dump(),
                        'liveness': 'available' if worker_monitor is not None else 'unavailable',
                        'slots': [
                            {
                                'slot': slot.slot,
                                'pool': slot.pool,
                                'state': slot.state,
                                'observed_at': slot.observed_at.isoformat(),
                                'error': slot.error,
                                'job_id': slot.job_id,
                                'job_kind': slot.job_kind,
                            }
                            for slot in slots
                        ],
                    },
                    'summary': {
                        'running': len(active_job_ids) if worker_monitor is not None else durable_running_count,
                        'ready': total_active
                        - (len(active_job_ids) if worker_monitor is not None else durable_running_count)
                        - retry_wait_count,
                        'retry_wait': retry_wait_count,
                    },
                    'total_jobs': total_active,
                    'jobs': [
                        {
                            'job_id': job.id,
                            'kind': job.kind,
                            'state': 'running' if job.id in active_job_ids else job.state,
                            'queue_state': (
                                'retry_wait'
                                if job.state == 'queued'
                                and job.next_attempt_at is not None
                                and job.next_attempt_at > observed_at
                                else 'ready'
                            ),
                            'source_id': job.source_id,
                            'library_record_id': job.library_record_id,
                            'release_mbid': job.release_mbid,
                            'target': source_target(job),
                            'created_at': job.created_at.isoformat(),
                            'next_attempt_at': job.next_attempt_at.isoformat()
                            if job.next_attempt_at is not None
                            else None,
                            'attempt_count': len(job.attempts),
                        }
                        for job in jobs
                    ],
                }
            )

    return router
