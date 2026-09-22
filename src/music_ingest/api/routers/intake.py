from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, status

from music_ingest.api.dependencies import SessionFactory
from music_ingest.contracts import ScanJobResponse
from music_ingest.repositories.jobs import JobRepository


def create_router(session_factory: SessionFactory) -> APIRouter:
    router = APIRouter()

    @router.post('/api/intake/notification', response_model=ScanJobResponse, status_code=status.HTTP_202_ACCEPTED)
    def change_notification() -> ScanJobResponse:
        with session_factory() as session:
            job, _ = JobRepository(session).enqueue_reconciliation_scan(datetime.now(UTC))
            session.commit()
            return ScanJobResponse(job_id=job.id, state=job.state)

    return router
