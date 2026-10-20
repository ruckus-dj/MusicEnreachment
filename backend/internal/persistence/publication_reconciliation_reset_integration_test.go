//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestOutputResetCancelsPendingPublicationReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationResetFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, f.epoch, []uuid.UUID{f.position}, []string{"reset-test"})
	}); err != nil {
		t.Fatal(err)
	}
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("dispatch=%d err=%v", count, err)
	}
	var eventID uuid.UUID
	var operationID uuid.UUID
	var jobID int64
	if err := f.db.NewRaw(`SELECT id,operation_id,registered_job_id FROM publication_reconciliation_event WHERE position_id=?`, f.position).Scan(ctx, &eventID, &operationID, &jobID); err != nil {
		t.Fatal(err)
	}
	if jobID <= 0 {
		t.Fatalf("registered job id = %d", jobID)
	}
	if _, err := f.reset(); err != nil {
		t.Fatal(err)
	}
	var jobs, events, requests int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE id=? AND state='cancelled'`, jobID).Scan(ctx, &jobs); err != nil || jobs != 1 {
		t.Fatalf("cancelled reconciliation jobs=%d err=%v", jobs, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE id=?`, eventID).Scan(ctx, &events); err != nil || events != 0 {
		t.Fatalf("old reconciliation events=%d err=%v", events, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, f.epoch, f.position).Scan(ctx, &requests); err != nil || requests != 0 {
		t.Fatalf("old reconciliation requests=%d err=%v", requests, err)
	}
	var state, stage string
	var safeError string
	if err := f.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, operationID).Scan(ctx, &state, &stage, &safeError); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "finished" || safeError != outputResetSafeError {
		t.Fatalf("reconciliation operation = %q/%q/%q", state, stage, safeError)
	}
}

func TestOutputResetBlocksClaimedReconciliationDeliveryOnTerminalOperationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationResetFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, f.epoch, []uuid.UUID{f.position}, []string{"claimed-reset-test"})
	}); err != nil {
		t.Fatal(err)
	}
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("dispatch=%d err=%v", count, err)
	}
	operationID := inserter.args[0].OperationID
	jobID := inserter.ids[0]
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, operationID, jobID, 1); err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='failed',safe_error='fixture',finished_at=now() WHERE id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reset(); err == nil {
		t.Fatal("reset ignored claimed reconciliation delivery on terminal operation")
	}
	f.assertUnchanged(t, true)
	var claimed bool
	if err := f.db.NewRaw(`SELECT delivery_claimed FROM publication_reconciliation_event WHERE operation_id=?`, operationID).Scan(ctx, &claimed); err != nil || !claimed {
		t.Fatalf("delivery claimed=%t err=%v", claimed, err)
	}
}
