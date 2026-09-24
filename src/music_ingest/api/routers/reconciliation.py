from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException, status

from music_ingest.api.dependencies import SessionFactory
from music_ingest.contracts import (
    PublicationReconciliationJobResponse,
    PublicationReconciliationResult,
    ScanJobResponse,
    ScanResult,
)
from music_ingest.models import JobRecord
from music_ingest.repositories.jobs import JobRepository


def create_router(session_factory: SessionFactory) -> APIRouter:
    router = APIRouter()

    @router.post('/api/reconciliation/scan', response_model=ScanJobResponse, status_code=status.HTTP_202_ACCEPTED)
    def reconciliation_scan() -> ScanJobResponse:
        with session_factory() as session:
            job, _ = JobRepository(session).enqueue_reconciliation_scan(datetime.now(UTC))
            session.commit()
            return ScanJobResponse(job_id=job.id, state=job.state)

    @router.get('/api/reconciliation/scan/{job_id}', response_model=ScanJobResponse)
    def reconciliation_scan_status(job_id: str) -> ScanJobResponse:
        with session_factory() as session:
            job = session.get(JobRecord, job_id)
            if job is None or job.kind != 'reconciliation_scan':
                raise HTTPException(status_code=404, detail='reconciliation scan job not found')
            result = ScanResult.model_validate_json(job.result_json) if job.result_json is not None else None
            return ScanJobResponse(job_id=job.id, state=job.state, result=result)

    @router.post(
        '/api/reconciliation/publications',
        response_model=PublicationReconciliationJobResponse,
        status_code=status.HTTP_202_ACCEPTED,
    )
    def publication_reconciliation() -> PublicationReconciliationJobResponse:
        with session_factory() as session:
            job, _ = JobRepository(session).enqueue_publication_reconciliation(datetime.now(UTC))
            session.commit()
            return PublicationReconciliationJobResponse(job_id=job.id, state=job.state)

    @router.get('/api/reconciliation/publications/{job_id}', response_model=PublicationReconciliationJobResponse)
    def publication_reconciliation_status(job_id: str) -> PublicationReconciliationJobResponse:
        with session_factory() as session:
            job = session.get(JobRecord, job_id)
            if job is None or job.kind != 'publication_reconciliation':
                raise HTTPException(status_code=404, detail='publication reconciliation job not found')
            result = (
                PublicationReconciliationResult.model_validate_json(job.result_json)
                if job.result_json is not None
                else None
            )
            return PublicationReconciliationJobResponse(job_id=job.id, state=job.state, result=result)

    return router
