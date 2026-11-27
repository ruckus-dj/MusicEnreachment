//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceAnalysisWorkArtifactRetirementWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigratedWithRiver(t)
	t.Run("removes_current_borrower_copy_and_binding", func(t *testing.T) {
		runRetirementRemovesCurrentBorrowerCopy(t, database)
	})
	t.Run("stale_delivery_never_removes_bound_copy", func(t *testing.T) {
		runRetirementRejectsStaleDelivery(t, database)
	})
	t.Run("removal_error_keeps_cleanup_failed_row_without_binding", func(t *testing.T) {
		runRetirementRecordsRemovalError(t, database)
	})
}

type artifactRetirementFixture struct {
	normalizedAnalysisFixture
	artifactID    uuid.UUID
	fence         persistence.SourceAnalysisArtifactFence
	workDirectory string
}

// prepareArtifactRetirement creates the real staged-borrower state: a running
// normalized delivery that owns a claimed step, an execution registry row in
// staged mode, and a ready canonical artifact bound to that exact delivery.
func prepareArtifactRetirement(t *testing.T, database *bun.DB) artifactRetirementFixture {
	t.Helper()
	ctx := context.Background()
	fixture := normalizedWorkOperationWithDatabase(t, database, t.TempDir(), true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	workDirectory := t.TempDir()
	if _, err := database.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES(?,?)
		ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value, updated_at=now()`,
		settings.WorkDirectoryKey, workDirectory).Exec(ctx); err != nil {
		t.Fatalf("seed staged work directory: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatal(err)
	}
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit staged analysis operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='running',stage='analysis',started_at=now() WHERE id=?`, operation.ID); err != nil {
		t.Fatal(err)
	}
	fence := persistence.SourceAnalysisArtifactFence{
		WorkID: fixture.work.ID, OperationID: operation.ID,
		OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID,
	}
	insertWorkExecution(t, ctx, fixture, fence, "staged")
	if _, err := fixture.repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: fence.WorkID, OperationID: fence.OperationID, OperationAttempt: fence.OperationAttempt,
		JobID: fence.JobID, Step: persistence.SourceStepSHA256,
	}); err != nil {
		t.Fatalf("claim staged analysis step: %v", err)
	}
	artifacts := persistence.NewSourceAnalysisArtifactRepository(database)
	artifactID := uuid.New()
	artifact, err := artifacts.Acquire(ctx, artifactID, fence)
	if err != nil {
		t.Fatalf("acquire staged artifact: %v", err)
	}
	ready, err := artifacts.MarkReady(ctx, artifactID, fence, artifact.SourceSizeBytes)
	if err != nil {
		t.Fatalf("mark staged artifact ready: %v", err)
	}
	wantPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(artifactID.String()) + ".flac"
	if ready.State != persistence.SourceAnalysisArtifactReady || ready.RelativeOutputPath != wantPath {
		t.Fatalf("ready artifact = state %q path %q, want ready %q", ready.State, ready.RelativeOutputPath, wantPath)
	}
	var borrower struct {
		OperationID uuid.UUID `bun:"borrower_operation_id,type:uuid"`
		Attempt     int       `bun:"borrower_operation_attempt"`
		JobID       int64     `bun:"borrower_job_id"`
	}
	if err := database.NewRaw(`SELECT borrower_operation_id,borrower_operation_attempt,borrower_job_id
		FROM source_analysis_work_artifact_binding WHERE work_id=?`, fixture.work.ID).Scan(ctx, &borrower); err != nil {
		t.Fatalf("read staged artifact borrower: %v", err)
	}
	if borrower.OperationID != fence.OperationID || borrower.Attempt != fence.OperationAttempt || borrower.JobID != fence.JobID {
		t.Fatalf("staged artifact borrower = %s/%d/%d, want %s/%d/%d", borrower.OperationID, borrower.Attempt,
			borrower.JobID, fence.OperationID, fence.OperationAttempt, fence.JobID)
	}
	return artifactRetirementFixture{
		normalizedAnalysisFixture: fixture, artifactID: artifactID, fence: fence, workDirectory: workDirectory,
	}
}

func runRetirementRemovesCurrentBorrowerCopy(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	fixture := prepareArtifactRetirement(t, database)
	removals := 0
	var removedDirectory string
	var removedArtifact persistence.SourceAnalysisArtifact
	err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, fixture.fence.WorkID,
		persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID},
		fixture.fence.OperationID,
		func(workDirectory string, artifact persistence.SourceAnalysisArtifact) error {
			removals++
			removedDirectory = workDirectory
			removedArtifact = artifact
			return nil
		})
	if err != nil {
		t.Fatalf("retire authorized staged artifact: %v", err)
	}
	if removals != 1 {
		t.Fatalf("removal callbacks = %d, want exactly one for the authorized borrower", removals)
	}
	if removedDirectory != fixture.workDirectory {
		t.Fatalf("removal work directory = %q, want %q", removedDirectory, fixture.workDirectory)
	}
	wantPath := "analysis/staging/" + strings.ToLower(fixture.root.ID.String()) + "/" +
		strings.ToLower(fixture.work.ID.String()) + "/" + strings.ToLower(fixture.artifactID.String()) + ".flac"
	if removedArtifact.ID != fixture.artifactID || removedArtifact.RelativeOutputPath != wantPath ||
		removedArtifact.State != persistence.SourceAnalysisArtifactReady {
		t.Fatalf("removed artifact = %+v, want ready canonical artifact %s at %q", removedArtifact, fixture.artifactID, wantPath)
	}
	assertArtifactRetirementRows(t, fixture, 0, 0)
}

func runRetirementRejectsStaleDelivery(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	fixture := prepareArtifactRetirement(t, database)
	removals := 0
	remove := func(string, persistence.SourceAnalysisArtifact) error {
		removals++
		return nil
	}
	deliveries := []struct {
		name        string
		workID      uuid.UUID
		operationID uuid.UUID
		delivery    persistence.SourceAnalysisOperationDelivery
	}{
		{name: "stale attempt", workID: fixture.fence.WorkID, operationID: fixture.fence.OperationID,
			delivery: persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt + 1, JobID: fixture.fence.JobID}},
		{name: "stale job", workID: fixture.fence.WorkID, operationID: fixture.fence.OperationID,
			delivery: persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID + 1}},
		{name: "unknown operation", workID: fixture.fence.WorkID, operationID: uuid.New(),
			delivery: persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID}},
	}
	for _, delivery := range deliveries {
		if err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, delivery.workID, delivery.delivery, delivery.operationID, remove); err != nil {
			t.Fatalf("%s retirement error = %v, want silent no-op", delivery.name, err)
		}
		if removals != 0 {
			t.Fatalf("%s invoked removal %d times, want zero before authorization", delivery.name, removals)
		}
		assertArtifactRetirementRows(t, fixture, 1, 1)
	}
	if err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, fixture.fence.WorkID,
		persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID},
		fixture.fence.OperationID, nil); err == nil {
		t.Fatal("retirement without a remover was accepted")
	}
	// Stale attempts must not consume the authorization or the binding: the
	// exact current borrower still retires the copy afterwards.
	if err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, fixture.fence.WorkID,
		persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID},
		fixture.fence.OperationID, remove); err != nil {
		t.Fatalf("retire after stale deliveries: %v", err)
	}
	if removals != 1 {
		t.Fatalf("removal callbacks = %d, want one after the authorized delivery", removals)
	}
	assertArtifactRetirementRows(t, fixture, 0, 0)
}

func runRetirementRecordsRemovalError(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	fixture := prepareArtifactRetirement(t, database)
	removalFailure := errors.New("injected staged artifact removal failure")
	removals := 0
	remove := func(string, persistence.SourceAnalysisArtifact) error {
		removals++
		return removalFailure
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: fixture.fence.OperationAttempt, JobID: fixture.fence.JobID}
	err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, fixture.fence.WorkID, delivery, fixture.fence.OperationID, remove)
	if !errors.Is(err, removalFailure) {
		t.Fatalf("retirement removal error = %v, want injected failure", err)
	}
	if removals != 1 {
		t.Fatalf("removal callbacks = %d, want one attempted removal", removals)
	}
	// The transaction releases the binding before invoking the remover, so a
	// failed filesystem removal never strands a borrower reference.
	assertArtifactRetirementRows(t, fixture, 0, 1)
	var cleanup struct {
		State        string  `bun:"state"`
		CleanupError *string `bun:"cleanup_error"`
	}
	if err := database.NewRaw(`SELECT state,cleanup_error FROM source_analysis_artifact WHERE id=?`, fixture.artifactID).Scan(ctx, &cleanup); err != nil {
		t.Fatalf("read failed-cleanup artifact: %v", err)
	}
	if cleanup.State != persistence.SourceAnalysisArtifactCleanupFailed || cleanup.CleanupError == nil || *cleanup.CleanupError == "" {
		t.Fatalf("artifact after removal error = state %q error %v, want cleanup_failed with a safe error", cleanup.State, cleanup.CleanupError)
	}
	// Repeating the exact delivery has no binding left to authorize another
	// removal; the cleanup_failed row is preserved for the cleanup worker.
	if err := fixture.repository.RetireSourceAnalysisWorkArtifact(ctx, fixture.fence.WorkID, delivery, fixture.fence.OperationID, remove); err != nil {
		t.Fatalf("repeat retirement after removal error: %v", err)
	}
	if removals != 1 {
		t.Fatalf("repeated retirement invoked removal %d times, want one", removals)
	}
	assertArtifactRetirementRows(t, fixture, 0, 1)
}

func assertArtifactRetirementRows(t *testing.T, fixture artifactRetirementFixture, wantBindings, wantArtifacts int) {
	t.Helper()
	ctx := context.Background()
	var bindings, artifacts int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_work_artifact_binding WHERE work_id=?`, fixture.work.ID).Scan(ctx, &bindings); err != nil {
		t.Fatalf("count artifact bindings: %v", err)
	}
	if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, fixture.artifactID).Scan(ctx, &artifacts); err != nil {
		t.Fatalf("count artifact rows: %v", err)
	}
	if bindings != wantBindings || artifacts != wantArtifacts {
		t.Fatalf("retirement rows = bindings %d artifacts %d, want %d and %d", bindings, artifacts, wantBindings, wantArtifacts)
	}
}
