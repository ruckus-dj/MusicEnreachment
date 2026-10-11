//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type failAfterMatchingReconcileInsert struct{ persistence.RiverInserter }

func (client failAfterMatchingReconcileInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := client.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	return result, errors.New("injected dispatch failure")
}

func TestDispatchPendingIncomingMatchingReconciliationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/matching-reconcile-dispatch", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis: %v", err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE operation SET state='running' WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	digest[0] = 1
	variantID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO media_variant (id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES (?,?,?,?,?,?)`, variantID, fixture.work.SizeBytes, digest, time.Now().UTC(), "SHA-256", operation.ID); err != nil {
		t.Fatalf("insert SHA result: %v", err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='sha256'`, variantID, fixture.work.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}
	if err := fixture.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "succeeded", "completed", ""); err != nil {
		t.Fatalf("settle analysis: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.database.NewSelect().Model(&event).Where("source_operation_id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatalf("read outbox event: %v", err)
	}
	client := openScanEnqueueRiver(t, fixture.database)
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, failAfterMatchingReconcileInsert{client}); err == nil {
		t.Fatal("expected injected dispatch failure")
	}
	storedEvent, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	event = *storedEvent
	if event.State != "pending" || event.RegisteredJobID != nil {
		t.Fatalf("failed dispatch was not rolled back: %+v", event)
	}
	var jobs int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("failed dispatch left %d River jobs", jobs)
	}
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch retry: %v", err)
	}
	storedEvent, err = fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	event = *storedEvent
	if event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID < 1 {
		t.Fatalf("event not marked with job identity: %+v", event)
	}
	var args persistence.IncomingMatchingReconcileJobArgs
	var rawArgs string
	if err := fixture.database.NewRaw(`SELECT args FROM river_job WHERE id=?`, *event.RegisteredJobID).Scan(ctx, &rawArgs); err != nil {
		t.Fatalf("read River args: %v", err)
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("decode River args: %v", err)
	}
	if args.EventID != event.ID {
		t.Fatalf("job references event %s, want %s", args.EventID, event.ID)
	}
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("repeat dispatch: %v", err)
	}
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("repeat dispatch created %d reconciliation jobs, want 1", jobs)
	}
}
