//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestRecoverInterruptedNormalizedSourceAnalysisReturnsOnlyOwnedUnfinishedStepsToPendingWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-recovery")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/recovery.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)

	probeID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES (?,?,?,?::jsonb,?,?,?, ?,1)`, probeID, location.SizeBytes, "ffprobe version 7.1.2", `{"format":{"format_name":"flac"}}`, persistence.SourceAnalysisPolicyVersion, `{}`, time.Now().UTC(), uuid.New()); err != nil {
		t.Fatalf("insert successful result: %v", err)
	}
	priorFailure := "previous fingerprint failure"
	work := normalizedWork(t, ctx, repository, root, location, true)
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step (work_id,step,state) VALUES (?,'sha256','pending')`, work.ID); err != nil {
		t.Fatalf("insert pending SHA step: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step (work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, work.ID, probeID); err != nil {
		t.Fatalf("insert successful probe step: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step (work_id,step,state,safe_error) VALUES (?,'fingerprint','failed',?)`, work.ID, priorFailure); err != nil {
		t.Fatalf("insert failed fingerprint step: %v", err)
	}
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized operation: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted operation has no River job")
	}
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing' WHERE id=?`, operation.ID); err != nil {
		t.Fatalf("mark operation running: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='running',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=? WHERE work_id=? AND step='sha256'`, operation.ID, operation.Attempt, *operation.RiverJobID, work.ID); err != nil {
		t.Fatalf("mark step as delivered: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, *operation.RiverJobID); err != nil {
		t.Fatalf("mark River delivery terminal: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 0)

	setupRepository := persistence.NewSetupManagerRepository(database)
	if err := setupRepository.RecoverInterruptedSourceAnalysis(ctx, operation.ID, "The source analysis was interrupted."); err != nil {
		t.Fatalf("recover normalized operation: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 0, 0)

	var steps []persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&steps).Where("work_id=?", work.ID).Order("step").Scan(ctx); err != nil {
		t.Fatalf("read recovered steps: %v", err)
	}
	byName := make(map[string]persistence.SourceAnalysisStep, len(steps))
	for _, step := range steps {
		byName[step.Step] = step
	}
	sha := byName[string(persistence.SourceStepSHA256)]
	if sha.State != "pending" || sha.ExecutionOperationID != nil || sha.ExecutionOperationAttempt != nil || sha.ExecutionJobID != nil {
		t.Fatalf("interrupted step was not returned to pending without its fence: %+v", sha)
	}
	probe := byName[string(persistence.SourceStepProbe)]
	if probe.State != "succeeded" || probe.SuccessProbeVariantID == nil || *probe.SuccessProbeVariantID != probeID {
		t.Fatalf("successful result selection changed: %+v", probe)
	}
	fingerprint := byName[string(persistence.SourceStepFingerprint)]
	if fingerprint.State != "failed" || fingerprint.SafeError == nil || *fingerprint.SafeError != priorFailure {
		t.Fatalf("failed sibling changed during recovery: %+v", fingerprint)
	}
	stored, err := setupRepository.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read recovered operation: %v", err)
	}
	if stored.State != "failed" || stored.Stage != "recovered" || stored.ToolsReadRequired || stored.TargetSourceRootID != nil || stored.SafeError == nil || *stored.SafeError != "The source analysis was interrupted." {
		t.Fatalf("unexpected recovered operation: %+v", stored)
	}
}

func TestRecoverInterruptedNormalizedSourceAnalysisDoesNotClearLiveNewDeliveryWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-recovery-fence")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/fence.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true, persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"})
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized operation: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted operation has no River job")
	}
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='running' WHERE id=?`, operation.ID); err != nil {
		t.Fatalf("mark operation running: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='running',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=? WHERE work_id=? AND step='sha256'`, operation.ID, operation.Attempt, *operation.RiverJobID, work.ID); err != nil {
		t.Fatalf("mark step as delivered: %v", err)
	}
	if err := persistence.NewSetupManagerRepository(database).RecoverInterruptedSourceAnalysis(ctx, operation.ID, "interrupted"); err != nil {
		t.Fatalf("check live delivery during recovery: %v", err)
	}
	var stored persistence.Operation
	if err := database.NewSelect().Model(&stored).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	var step persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&step).Where("work_id=? AND step='sha256'", work.ID).Scan(ctx); err != nil {
		t.Fatalf("read step: %v", err)
	}
	if stored.State != "running" || stored.Attempt != operation.Attempt || stored.RiverJobID == nil || *stored.RiverJobID != *operation.RiverJobID {
		t.Fatalf("live operation delivery was modified: %+v", stored)
	}
	if step.State != "running" || step.ExecutionOperationID == nil || *step.ExecutionOperationID != operation.ID || step.ExecutionOperationAttempt == nil || *step.ExecutionOperationAttempt != operation.Attempt || step.ExecutionJobID == nil || *step.ExecutionJobID != *operation.RiverJobID {
		t.Fatalf("live step fence was modified: %+v", step)
	}
}
