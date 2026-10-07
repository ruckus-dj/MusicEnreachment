//go:build integration

package persistence_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestRecoveredFingerprintRerunAdmissionUsesOriginalIntentWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/recovered-fingerprint-rerun")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/recovered.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	priorResultID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_result
		(id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, priorResultID, "1.5.1", "fpcalc version 1.5.1", "chromaprint", 1, "AQAA", 180.0, time.Now().UTC(), uuid.New(), 1); err != nil {
		t.Fatalf("insert prior successful fingerprint result: %v", err)
	}
	work := normalizedWork(t, ctx, repository, root, location, false)
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step (work_id,step,state,success_fingerprint_result_id,success_reuse_origin) VALUES (?,'fingerprint','succeeded',?,'executed')`, work.ID, priorResultID); err != nil {
		t.Fatalf("insert prior successful fingerprint step: %v", err)
	}

	installation := insertAnalysisFPCalcFixture(t, ctx, database, "fpcalc-original-release", "1.5.1")
	originalTool := normalizedToolSelection(t, ctx, database, installation.ID, "fpcalc")
	step := string(persistence.SourceStepFingerprint)
	interrupted := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep,
		&work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{originalTool})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, interrupted, client, service.SourceAnalysisJobArgs{OperationID: interrupted.ID}, nil); err != nil {
		t.Fatalf("admit original fingerprint rerun: %v", err)
	}
	if interrupted.RiverJobID == nil {
		t.Fatal("admitted fingerprint rerun has no River job")
	}
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='running',stage='fingerprinting',started_at=now() WHERE id=?`, interrupted.ID); err != nil {
		t.Fatalf("mark fingerprint rerun running: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='running',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=? WHERE work_id=? AND step='fingerprint'`, interrupted.ID, interrupted.Attempt, *interrupted.RiverJobID, work.ID); err != nil {
		t.Fatalf("mark fingerprint rerun delivered: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *interrupted.RiverJobID); err != nil {
		t.Fatalf("finalize interrupted fingerprint job: %v", err)
	}
	if err := repository.RecoverNormalizedSourceAnalysisDelivery(ctx, interrupted.ID, persistence.SourceAnalysisOperationDelivery{
		Attempt: interrupted.Attempt, JobID: *interrupted.RiverJobID,
	}, "The source analysis was interrupted."); err != nil {
		t.Fatalf("recover interrupted fingerprint rerun: %v", err)
	}
	var recoveredStep persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&recoveredStep).Where("work_id=? AND step='fingerprint'", work.ID).Scan(ctx); err != nil {
		t.Fatalf("read recovered fingerprint step: %v", err)
	}
	if recoveredStep.State != "pending" || recoveredStep.LastOperationID == nil || *recoveredStep.LastOperationID != interrupted.ID || len(recoveredStep.InputSnapshot) == 0 ||
		recoveredStep.ExecutionOperationID != nil || recoveredStep.ExecutionOperationAttempt != nil || recoveredStep.ExecutionJobID != nil {
		t.Fatalf("recovery did not preserve pending fingerprint intent without its execution fence: %+v", recoveredStep)
	}
	assertOperationHoldCounts(t, ctx, database, interrupted.ID, 0, 0)
	retainedInput := string(recoveredStep.InputSnapshot)
	// Retained intent is self-contained and does not depend on keeping operation history.
	if _, err := database.ExecContext(ctx, `DELETE FROM operation WHERE id=?`, interrupted.ID); err != nil {
		t.Fatalf("dismiss old operation history: %v", err)
	}

	// A request with another executable version cannot replace the version pinned
	// by the interrupted operation.
	changedTool := originalTool
	changedTool.Version = "1.5.0"
	changed := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep,
		&work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{changedTool})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, changed, client, service.SourceAnalysisJobArgs{OperationID: changed.ID}, nil); err == nil {
		t.Fatal("rerun admission replaced the original pinned tool version")
	}

	// A plain pending state has no recovered durable intent and remains excluded.
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET last_operation_id=NULL,input_snapshot=NULL WHERE work_id=? AND step='fingerprint'`, work.ID); err != nil {
		t.Fatal(err)
	}
	ordinary := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep,
		&work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{originalTool})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, ordinary, client, service.SourceAnalysisJobArgs{OperationID: ordinary.ID}, nil); err == nil {
		t.Fatal("ordinary pending fingerprint was accepted as a recovered rerun")
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET input_snapshot=?::jsonb,last_operation_id=? WHERE work_id=? AND step='fingerprint'`, retainedInput, interrupted.ID, work.ID); err != nil {
		t.Fatal(err)
	}

	operations := []*persistence.Operation{
		normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{originalTool}),
		normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{originalTool}),
	}
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wait sync.WaitGroup
	for _, operation := range operations {
		wait.Add(1)
		go func(operation *persistence.Operation) {
			defer wait.Done()
			<-start
			results <- repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil)
		}(operation)
	}
	close(start)
	wait.Wait()
	close(results)
	accepted := 0
	var admissionErrors []string
	for err := range results {
		if err == nil {
			accepted++
		} else {
			admissionErrors = append(admissionErrors, err.Error())
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent recovered rerun admissions accepted %d, want exactly one; returned errors: %v", accepted, admissionErrors)
	}
	var state, selectedStep string
	if err := database.NewRaw(`SELECT state,step FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &state, &selectedStep); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || selectedStep != string(persistence.SourceStepFingerprint) {
		t.Fatalf("recovered rerun queued state=%q step=%q", state, selectedStep)
	}
	var rerunJobs int
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE kind='analyze_source' AND state='queued' AND target_work_id=? AND target_step='fingerprint'`, work.ID).Scan(ctx, &rerunJobs); err != nil {
		t.Fatal(err)
	}
	if rerunJobs != 1 {
		t.Fatalf("queued fingerprint rerun operations = %d, want 1", rerunJobs)
	}
}
