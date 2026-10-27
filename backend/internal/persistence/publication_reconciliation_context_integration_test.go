//go:build integration

package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestResolvePublicationReconciliationContextReportsRetainedFailedAttemptPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"metadata", "audio"})
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("dispatch=%d err=%v", count, err)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}
	assertContext := func(wantActive bool) {
		t.Helper()
		got, err := repo.ResolvePublicationReconciliationContext(ctx, guard)
		if err != nil {
			t.Fatal(err)
		}
		if got.ReleaseID == uuid.Nil || got.RequestedGeneration != 1 || got.HasCurrentPublication || got.HasActiveAttempt != wantActive {
			t.Fatalf("context release=%s generation=%d current=%v active=%v", got.ReleaseID, got.RequestedGeneration, got.HasCurrentPublication, got.HasActiveAttempt)
		}
		if len(got.DirtyScopes) != 2 || got.DirtyScopes[0].Scope != "audio" || got.DirtyScopes[1].Scope != "metadata" || got.DirtyScopes[0].Generation != 1 || got.DirtyScopes[1].Generation != 1 {
			t.Fatalf("dirty scopes=%+v", got.DirtyScopes)
		}
	}
	assertContext(false)
	if parked, err := repo.ParkPublicationReconciliationBusyAttempt(ctx, guard, 1, PublicationReconciliationBusyAttemptReason); err != nil || parked {
		t.Fatalf("park without busy attempt=%v err=%v", parked, err)
	}

	// Retained failed attempts are still exclusive publication claims; their
	// operation state must not make the context report the position as free.
	publicationID, generation := uuid.New(), uuid.New()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at) VALUES (?,'publish_track_v1','failed','failed',jsonb_build_object('version',1,'position_id',?::text,'output_epoch',?::text,'intent_kind','audio'),'fixture',now())`, publicationID, position, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO track_publication_attempt(position_id,operation_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','preparing','{}'::jsonb,'{}'::jsonb)`, position, publicationID, epoch, generation); err != nil {
		t.Fatal(err)
	}
	assertContext(true)
	if _, err := repo.db.ExecContext(ctx, `UPDATE operation SET state='queued',stage='queued',safe_error=NULL,finished_at=NULL WHERE id=?`, publicationID); err != nil {
		t.Fatal(err)
	}
	if parked, err := repo.ParkPublicationReconciliationBusyAttempt(ctx, guard, 1, PublicationReconciliationBusyAttemptReason); err != nil || !parked {
		t.Fatalf("park retained attempt=%v err=%v", parked, err)
	}
	var evaluated, reconciled, dirty int
	var reason string
	if err := repo.db.NewRaw(`SELECT evaluated_generation,reconciled_generation,parked_reason FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &evaluated, &reconciled, &reason); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if evaluated != 1 || reconciled != 0 || reason != "publication attempt is retained" || dirty != 2 {
		t.Fatalf("parked request evaluated=%d reconciled=%d reason=%q dirty=%d", evaluated, reconciled, reason, dirty)
	}

	// The context read is fenced by the actual River row, not just its earlier claim.
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET attempt=2 WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolvePublicationReconciliationContext(ctx, guard); err == nil {
		t.Fatal("stale River attempt resolved a publication context")
	}
}

func TestPublicationReconciliationDispatcherWakesSettledBusyAttemptPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"audio", "metadata"})
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("initial dispatch=%d err=%v", count, err)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, outcome, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || outcome != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", outcome, err)
	}
	publicationID, generation := uuid.New(), uuid.New()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at) VALUES (?,'publish_track_v1','failed','failed',jsonb_build_object('version',1,'position_id',?::text,'output_epoch',?::text,'intent_kind','audio'),'fixture',now())`, publicationID, position, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO track_publication_attempt(position_id,operation_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','preparing','{}'::jsonb,'{}'::jsonb)`, position, publicationID, epoch, generation); err != nil {
		t.Fatal(err)
	}
	if parked, err := repo.ParkPublicationReconciliationBusyAttempt(ctx, guard, 1, PublicationReconciliationBusyAttemptReason); err != nil || !parked {
		t.Fatalf("park retained attempt=%v err=%v", parked, err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM track_publication_attempt WHERE operation_id=?`, publicationID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if dispatched, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || dispatched != 1 {
		t.Fatalf("dispatch after alternate attempt settlement=%d err=%v", dispatched, err)
	}
	var requested, dirty int
	if err := repo.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &requested); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if requested != 2 || dirty != 2 {
		t.Fatalf("fallback wake generation=%d dirty=%d", requested, dirty)
	}
}
