from __future__ import annotations

from dataclasses import dataclass

from music_ingest.models.jobs import ClaimedJob
from music_ingest.processing.execution import ExecutionContext
from music_ingest.reconciliation import apply_reconciliation_plan, load_reconciliation_snapshot, plan_reconciliation


@dataclass(frozen=True, slots=True)
class ReconciliationHandler:
    def handle(self, claimed: ClaimedJob, context: ExecutionContext) -> None:
        snapshot = load_reconciliation_snapshot(context.session, context.now)
        plan = plan_reconciliation(snapshot)
        claimed.job.result_json = apply_reconciliation_plan(context.session, plan, context.now).model_dump_json()
