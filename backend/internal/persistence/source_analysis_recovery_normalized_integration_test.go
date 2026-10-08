//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
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
	if _, _, _, err := repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "", ""); err != nil {
		t.Fatalf("start normalized delivery: %v", err)
	}
	if _, err := repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim normalized SHA step: %v", err)
	}
	artifactID := uuid.New()
	artifactPath := "analysis/staging/" + root.ID.String() + "/" + work.ID.String() + "/" + artifactID.String()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'ready')`, artifactID, work.ID, artifactPath, work.SizeBytes, work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert retained artifact: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id,artifact_id,borrower_operation_id,borrower_operation_attempt,borrower_job_id,requested_steps)
		VALUES (?,?,?,?,?,ARRAY['sha256'])`, work.ID, artifactID, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert artifact borrower: %v", err)
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
	var binding struct {
		ArtifactID               uuid.UUID  `bun:"artifact_id,type:uuid"`
		BorrowerOperationID      *uuid.UUID `bun:"borrower_operation_id,type:uuid,nullzero"`
		BorrowerOperationAttempt *int       `bun:"borrower_operation_attempt,nullzero"`
		BorrowerJobID            *int64     `bun:"borrower_job_id,nullzero"`
	}
	if err := database.NewRaw(`SELECT artifact_id,borrower_operation_id,borrower_operation_attempt,borrower_job_id FROM source_analysis_work_artifact_binding WHERE work_id=?`, work.ID).Scan(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ArtifactID != artifactID || binding.BorrowerOperationID != nil || binding.BorrowerOperationAttempt != nil || binding.BorrowerJobID != nil {
		t.Fatalf("recovery did not release exact borrower while retaining its binding: %+v", binding)
	}
	var artifact persistence.SourceAnalysisArtifact
	if err := database.NewSelect().Model(&artifact).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if artifact.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("recovery changed retained artifact state to %q", artifact.State)
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
	if _, _, _, err := repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "", ""); err != nil {
		t.Fatalf("start normalized delivery: %v", err)
	}
	if _, err := repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim normalized SHA step: %v", err)
	}
	artifactID := uuid.New()
	artifactPath := "analysis/staging/" + root.ID.String() + "/" + work.ID.String() + "/" + artifactID.String()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'ready')`, artifactID, work.ID, artifactPath, work.SizeBytes, work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert retained artifact: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id,artifact_id,borrower_operation_id,borrower_operation_attempt,borrower_job_id,requested_steps)
		VALUES (?,?,?,?,?,ARRAY['sha256'])`, work.ID, artifactID, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert artifact borrower: %v", err)
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
	var borrowerOperation uuid.UUID
	if err := database.NewRaw(`SELECT borrower_operation_id FROM source_analysis_work_artifact_binding WHERE work_id=?`, work.ID).Scan(ctx, &borrowerOperation); err != nil {
		t.Fatal(err)
	}
	if borrowerOperation != operation.ID {
		t.Fatalf("live delivery borrower was released: got %s want %s", borrowerOperation, operation.ID)
	}
}

func TestRecoverInterruptedNormalizedSourceAnalysisCleansCompletedArtifactAfterCrashWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-recovery-complete")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/complete.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
	)
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized operation: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted operation has no River job")
	}
	digest := make([]byte, 32)
	digest[0] = 1
	resultID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
		VALUES (?,?,?,?,?,?)`, resultID, work.SizeBytes, digest, time.Now().UTC(), "SHA-256", operation.ID); err != nil {
		t.Fatalf("insert successful SHA result: %v", err)
	}
	if _, _, _, err := repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "", ""); err != nil {
		t.Fatalf("start normalized delivery: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='executed',execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL WHERE work_id=? AND step='sha256'`, resultID, work.ID); err != nil {
		t.Fatalf("commit successful SHA result: %v", err)
	}
	artifactID := uuid.New()
	artifactPath := "analysis/staging/" + root.ID.String() + "/" + work.ID.String() + "/" + artifactID.String()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'ready')`, artifactID, work.ID, artifactPath, work.SizeBytes, work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert retained artifact: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id,artifact_id,borrower_operation_id,borrower_operation_attempt,borrower_job_id,requested_steps)
		VALUES (?,?,?,?,?,ARRAY['sha256'])`, work.ID, artifactID, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert artifact borrower: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, *operation.RiverJobID); err != nil {
		t.Fatalf("mark River delivery terminal: %v", err)
	}
	if err := persistence.NewSetupManagerRepository(database).RecoverInterruptedSourceAnalysis(ctx, operation.ID, "interrupted"); err != nil {
		t.Fatalf("recover normalized operation: %v", err)
	}
	var artifact persistence.SourceAnalysisArtifact
	if err := database.NewSelect().Model(&artifact).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatalf("read recovered artifact: %v", err)
	}
	if artifact.State != persistence.SourceAnalysisArtifactCleanupEligible {
		t.Fatalf("completed artifact state = %q, want cleanup eligible", artifact.State)
	}
	var bindings int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_work_artifact_binding WHERE work_id=?`, work.ID).Scan(ctx, &bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("completed artifact binding count = %d, want 0", bindings)
	}
}

func TestRecoverInterruptedNormalizedSourceAnalysisOldFenceCannotRetireNewBorrowerWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-recovery-new-borrower")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/new-borrower.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
	)
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized operation: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted operation has no River job")
	}
	insertNormalizedStagedExecution(t, ctx, database, work.ID, operation.ID, operation.Attempt, *operation.RiverJobID)
	artifactID := uuid.New()
	artifactPath := "analysis/staging/" + root.ID.String() + "/" + work.ID.String() + "/" + artifactID.String()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'ready')`, artifactID, work.ID, artifactPath, work.SizeBytes, work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert retained artifact: %v", err)
	}
	newAttempt := operation.Attempt + 1
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step
		SET state='pending',execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL
		WHERE work_id=? AND step='sha256'`, work.ID); err != nil {
		t.Fatalf("clear retired step fence before advancing operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE operation SET attempt=? WHERE id=?`, newAttempt, operation.ID); err != nil {
		t.Fatalf("advance operation attempt: %v", err)
	}
	insertNormalizedStagedExecution(t, ctx, database, work.ID, operation.ID, newAttempt, *operation.RiverJobID)
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id,artifact_id,borrower_operation_id,borrower_operation_attempt,borrower_job_id,requested_steps)
		VALUES (?,?,?,?,?,ARRAY['sha256'])`, work.ID, artifactID, operation.ID, newAttempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert new artifact borrower: %v", err)
	}
	err := repository.RecoverNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "interrupted")
	if !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("recover old delivery error = %v, want stale delivery", err)
	}
	var binding struct {
		BorrowerOperationAttempt int `bun:"borrower_operation_attempt"`
	}
	if err := database.NewRaw(`SELECT borrower_operation_attempt FROM source_analysis_work_artifact_binding WHERE work_id=?`, work.ID).Scan(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.BorrowerOperationAttempt != newAttempt {
		t.Fatalf("new borrower attempt = %d, want %d", binding.BorrowerOperationAttempt, newAttempt)
	}
	var artifact persistence.SourceAnalysisArtifact
	if err := database.NewSelect().Model(&artifact).Where("id=?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if artifact.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("old delivery changed new borrower's artifact to %q", artifact.State)
	}
}

func insertNormalizedStagedExecution(t *testing.T, ctx context.Context, database *bun.DB, workID, operationID uuid.UUID, attempt int, jobID int64) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?,?,?,?,'staged')`,
		workID, operationID, attempt, jobID); err != nil {
		t.Fatalf("insert staged execution registry fixture: %v", err)
	}
}
