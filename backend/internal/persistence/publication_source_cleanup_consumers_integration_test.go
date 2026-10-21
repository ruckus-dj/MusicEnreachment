//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func seedTerminalAnalysisDeliveryForCleanup(t *testing.T, f publicationExecutionFixture, workID uuid.UUID) (uuid.UUID, int64) {
	t.Helper()
	ctx := context.Background()
	var rootID uuid.UUID
	if err := f.db.NewRaw(`SELECT source_root_id FROM source_analysis_work WHERE id=?`, workID).Scan(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	operation := sourceCleanupTestAnalysisOperation(t, workID, rootID)
	operationID := operation.ID
	args, err := json.Marshal(sourceCleanupAnalysisJobArgs{OperationID: operationID})
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := f.db.NewRaw(`INSERT INTO river_job(kind,args,state,attempt,attempted_at) VALUES ('analyze_source_v1',?::jsonb,'running',1,now()) RETURNING id`, string(args)).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	// This terminal history deliberately retains no step triples or input holds;
	// its durable snapshot and mode nevertheless match the normalized contract.
	// Model the durable state after a generic terminal transition: step triples
	// and input holds are gone, but the actual native delivery has not returned.
	if _, err := f.db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id,safe_error,finished_at,source_analysis_mode)
		VALUES (?,'analyze_source','failed','failed',?::jsonb,1,?,'terminal operation with live native delivery',now(),'batch')`, operationID, string(operation.InputSnapshot), jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return operationID, jobID
}

func TestPublicationSourceCleanupDetectsStepHistoryOutsideSnapshotWorkPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, variantID := publicationSourceCleanupReadyFixture(t)
	var workID, rootID uuid.UUID
	if err := f.db.NewRaw(`SELECT w.id,w.source_root_id FROM source_work_variant_identity i JOIN source_analysis_work w ON w.id=i.work_id WHERE i.media_variant_id=?`, variantID).Scan(ctx, &workID, &rootID); err != nil {
		t.Fatal(err)
	}
	otherWorkID := uuid.New()
	otherLocationID := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
		SELECT ?,source_root_id,relative_path || '-history-only',size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id FROM source_location WHERE id IN (SELECT location_id FROM source_analysis_work WHERE id=?)`, otherLocationID, workID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id)
		SELECT ?,?,source_root_id,configured_path,inventory_path,relative_path || '-history-only',size_bytes,mtime,sha256_enabled,origin_scan_operation_id,? FROM source_analysis_work WHERE id=?`, otherWorkID, otherLocationID, otherLocationID, workID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	operationID, jobID := seedTerminalAnalysisDeliveryForCleanup(t, f, otherWorkID)
	// Point a legal pending SHA-256 step at history whose normalized snapshot
	// targets another work. The job has finished and terminal state has no holds.
	if _, err := f.db.NewRaw(`INSERT INTO source_analysis_step(work_id,step,state,step_attempt,last_operation_id) VALUES (?,'sha256','pending',0,?)`, workID, operationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if consumer, err := cleanupSourceConsumerExists(ctx, f.db, workID); err != nil || !consumer {
		t.Fatalf("live delivery linked only through step history was ignored: %t,%v", consumer, err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if consumer, err := cleanupSourceConsumerExists(ctx, f.db, workID); err != nil || consumer {
		t.Fatalf("completed unrelated operation history consumed step work: %t,%v", consumer, err)
	}
}

func TestPublicationSourceCleanupRejectsTerminalAnalysisWithLiveDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	var workID uuid.UUID
	if err := f.db.NewRaw(`SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?`, variantID).Scan(ctx, &workID); err != nil {
		t.Fatal(err)
	}
	if consumer, err := cleanupSourceConsumerExists(ctx, f.db, workID); err != nil || consumer {
		t.Fatalf("running publisher is not an analysis consumer: %t,%v", consumer, err)
	}
	_, jobID := seedTerminalAnalysisDeliveryForCleanup(t, f, workID)
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	for _, state := range []string{"available", "pending", "retryable", "scheduled", "running"} {
		t.Run(state, func(t *testing.T) {
			if _, err := f.db.NewRaw(`UPDATE river_job SET state=?,finalized_at=NULL WHERE id=?`, state, jobID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if consumer, err := cleanupSourceConsumerExists(ctx, f.db, workID); err != nil || !consumer {
				t.Fatalf("terminal operation/live %s consumer=%t,%v", state, consumer, err)
			}
			admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, &publicationSourceCleanupTestInserter{})
			if err != nil || admitted.OperationID != uuid.Nil || len(admitted.Blockers) == 0 {
				t.Fatalf("cleanup with live %s delivery=%+v,%v", state, admitted, err)
			}
		})
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if consumer, err := cleanupSourceConsumerExists(ctx, f.db, workID); err != nil || consumer {
		t.Fatalf("quiescent analysis history blocked cleanup: %t,%v", consumer, err)
	}
	if admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, &publicationSourceCleanupTestInserter{}); err != nil || admitted.OperationID == uuid.Nil {
		t.Fatalf("cleanup after analysis delivery completed=%+v,%v", admitted, err)
	}
}

func TestPublicationSourceCleanupExecutionRejectsOlderLiveAnalysisJobPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	guard, _ := admitAndClaimSourceCleanup(t, ctx, f, repo)
	var workID uuid.UUID
	if err := f.db.NewRaw(`SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?`, variantID).Scan(ctx, &workID); err != nil {
		t.Fatal(err)
	}
	validate := func() bool {
		t.Helper()
		valid := false
		if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			valid, err = validatePublicationSourceCleanupExecutionInputsTx(ctx, tx, guard)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return valid
	}
	if !validate() {
		t.Fatal("claimed cleanup baseline did not validate")
	}
	operationID, oldJobID := seedTerminalAnalysisDeliveryForCleanup(t, f, workID)
	var replacementJobID int64
	if err := f.db.NewRaw(`INSERT INTO river_job(kind,args,state,finalized_at) VALUES ('analyze_source_v1',jsonb_build_object('operation_id',?::text),'completed',now()) RETURNING id`, operationID).Scan(ctx, &replacementJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET river_job_id=? WHERE id=?`, replacementJobID, operationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if validate() {
		t.Fatal("older live analysis job was ignored after operation job replacement")
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, oldJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !validate() {
		t.Fatal("quiescent older analysis job still blocked cleanup")
	}
}
