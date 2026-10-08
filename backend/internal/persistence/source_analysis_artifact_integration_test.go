//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSourceAnalysisArtifactAcquireAndReadyFencingWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-fence", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{
		WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID,
	}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	claim := persistence.SourceStepClaim{WorkID: fence.WorkID, OperationID: fence.OperationID,
		OperationAttempt: fence.OperationAttempt, JobID: fence.JobID, Step: persistence.SourceStepSHA256}
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, claim); err != nil {
		t.Fatalf("claim analysis step: %v", err)
	}

	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire artifact: %v", err)
	}
	wantPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(artifactID.String())
	if artifact.RelativeOutputPath != wantPath || artifact.State != persistence.SourceAnalysisArtifactAcquiring || artifact.SourceSizeBytes != fixture.work.SizeBytes {
		t.Fatalf("acquired artifact = %+v, want canonical path %q and acquiring state", artifact, wantPath)
	}

	// A registered path collision owned by another artifact ID must make the
	// insert fail; Acquire never adopts or overwrites that row.
	collisionID, occupyingID := uuid.New(), uuid.New()
	collisionPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(collisionID.String())
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?,?,?,?,?,?,?,?, 'acquiring')`, occupyingID, fixture.work.ID, collisionPath, fixture.work.SizeBytes,
		fixture.work.Mtime, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
		t.Fatalf("insert path collision fixture: %v", err)
	}
	if _, err := artifacts.Acquire(ctx, collisionID, fence); err == nil {
		t.Fatal("acquire adopted an artifact path already registered to another artifact")
	}

	stale := fence
	stale.JobID++
	if _, err := artifacts.Acquire(ctx, uuid.New(), stale); err == nil {
		t.Fatal("stale delivery acquired artifact")
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, stale, artifact.SourceSizeBytes); err == nil {
		t.Fatal("stale delivery marked artifact ready")
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes-1); err == nil {
		t.Fatal("artifact became ready with a mismatched copied length")
	}
	ready, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes)
	if err != nil {
		t.Fatalf("mark exact-length artifact ready: %v", err)
	}
	if ready.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("artifact state after ready = %q, want ready", ready.State)
	}
	readyAgain, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes)
	if err != nil || readyAgain.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("idempotent ready = %+v, %v", readyAgain, err)
	}
	if err := artifacts.ForgetUncreated(ctx, artifactID, fence); err == nil {
		t.Fatal("ready artifact was forgotten as uncreated")
	}
}

func TestSourceAnalysisArtifactRejectsStaleWorkAndForgetsOnlyOwnedAcquiringRowWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-stale", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: fence.WorkID, OperationID: fence.OperationID, OperationAttempt: fence.OperationAttempt,
		JobID: fence.JobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim analysis step: %v", err)
	}
	artifactID := uuid.New()
	if _, err := artifacts.Acquire(ctx, artifactID, fence); err != nil {
		t.Fatalf("acquire artifact: %v", err)
	}

	wrongOwner := fence
	wrongOwner.OperationAttempt++
	if err := artifacts.ForgetUncreated(ctx, artifactID, wrongOwner); err == nil {
		t.Fatal("different operation attempt forgot artifact")
	}
	if err := artifacts.ForgetUncreated(ctx, artifactID, fence); err != nil {
		t.Fatalf("forget exact uncreated artifact: %v", err)
	}
	var count int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("artifact rows after exact forget = %d, want 0", count)
	}

	changed := fixture.work.SizeBytes + 1
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_location SET size_bytes=? WHERE id=?`, changed, fixture.location.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.Acquire(ctx, uuid.New(), fence); err == nil {
		t.Fatal("stale source stat acquired artifact")
	}
}

func TestSourceAnalysisExecutionModeIsImmutableAcrossRootModeChangesAndRetryWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/artifact-execution-mode", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis operation: %v", err)
	}
	started, _, mode, err := fixture.repository.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}, "", "")
	if err != nil {
		t.Fatalf("start staged delivery: %v", err)
	}
	if mode != "staged" || started.State != "running" {
		t.Fatalf("started delivery = mode %q state %q, want staged/running", mode, started.State)
	}
	fence := persistence.SourceAnalysisArtifactFence{WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
	claim := persistence.SourceStepClaim{WorkID: fence.WorkID, OperationID: fence.OperationID,
		OperationAttempt: fence.OperationAttempt, JobID: fence.JobID, Step: persistence.SourceStepSHA256}
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, claim); err != nil {
		t.Fatalf("claim staged SHA step: %v", err)
	}
	artifacts := persistence.NewSourceAnalysisArtifactRepository(fixture.database)
	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire artifact for staged execution: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='in_place' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	execution, err := fixture.repository.GetSourceAnalysisWorkExecution(ctx, fence)
	if err != nil || execution.ProcessingMode != "staged" {
		t.Fatalf("recorded execution after root mode change = %+v, %v; want staged", execution, err)
	}
	if _, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes); err != nil {
		t.Fatalf("mark staged artifact ready after root mode change: %v", err)
	}

	// A retry has a new attempt/job identity and records the then-current mode;
	// the retained artifact remains owned by its original immutable execution.
	retryFence := fence
	retryFence.OperationAttempt++
	retryFence.JobID++
	insertWorkExecution(t, ctx, fixture, retryFence, "in_place")
	retryExecution, err := fixture.repository.GetSourceAnalysisWorkExecution(ctx, retryFence)
	if err != nil || retryExecution.ProcessingMode != "in_place" {
		t.Fatalf("retry execution = %+v, %v; want in_place", retryExecution, err)
	}
	var stored persistence.SourceAnalysisArtifact
	if err := fixture.database.NewSelect().Model(&stored).Where("id = ?", artifactID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.OwnerOperationAttempt != fence.OperationAttempt || stored.OwnerJobID != fence.JobID || stored.OwnerOperationID != fence.OperationID {
		t.Fatalf("retained artifact origin changed after retry: %+v", stored)
	}
}

func insertWorkExecution(t *testing.T, ctx context.Context, fixture normalizedAnalysisFixture, fence persistence.SourceAnalysisArtifactFence, mode string) {
	t.Helper()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?,?,?,?,?)`,
		fence.WorkID, fence.OperationID, fence.OperationAttempt, fence.JobID, mode); err != nil {
		t.Fatalf("insert execution registry fixture: %v", err)
	}
}
