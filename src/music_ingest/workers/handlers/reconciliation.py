from __future__ import annotations

from dataclasses import dataclass

from music_ingest.repositories.jobs import ClaimedJob
from music_ingest.services.publication.locks import acquire_migration_lock, acquire_storage_lock
from music_ingest.services.publication.reconciliation import reconcile_publication_directory
from music_ingest.services.reconciliation import (
    apply_reconciliation_plan,
    load_reconciliation_snapshot,
    plan_reconciliation,
)
from music_ingest.workers.execution import ExecutionContext


@dataclass(frozen=True, slots=True)
class ReconciliationHandler:
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        snapshot = load_reconciliation_snapshot(context.session, context.now)
        plan = plan_reconciliation(snapshot)
        claimed.job.result_json = apply_reconciliation_plan(context.session, plan, context.now).model_dump_json()


@dataclass(frozen=True, slots=True)
class PublicationReconciliationHandler:
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        acquire_migration_lock(context.session, exclusive=True)
        acquire_storage_lock(context.session)
        claimed.job.result_json = reconcile_publication_directory(context.session, context.now).model_dump_json()
