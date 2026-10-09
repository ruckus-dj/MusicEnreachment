//go:build integration

package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceLocationDetailProjectsStagedArtifactFromCurrentDatabaseSnapshotWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newSourceAnalysisStartIntegration(t, true)
	details := persistence.NewSourceInventoryRepository(fixture.database)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_root SET processing_mode='staged' WHERE id=?`, fixture.root.ID); err != nil {
		t.Fatalf("set staged root execution mode: %v", err)
	}
	fixture.root.ProcessingMode = "staged"

	operation, err := fixture.starter.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID, Step: persistence.SourceStepSHA256,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	})
	if err != nil {
		t.Fatalf("admit normalized source analysis: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("normalized source analysis has no River job identity")
	}
	if _, _, mode, err := fixture.inventory.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID,
		persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID},
		fixture.platform.Platform.GOOS, fixture.platform.Platform.GOARCH); err != nil {
		t.Fatalf("start normalized source analysis delivery: %v", err)
	} else if mode != "staged" {
		t.Fatalf("delivery processing mode = %q, want staged", mode)
	}
	jobID := *operation.RiverJobID
	creatorID := operation.ID
	preparing, err := details.ReadSourceLocationDetail(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read staged preparation: %v", err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE source_root SET processing_mode='in_place' WHERE id=?`, fixture.root.ID).Exec(ctx); err != nil {
		t.Fatalf("change root execution mode: %v", err)
	}
	var executionMode string
	if err := fixture.database.NewRaw(`SELECT processing_mode FROM source_analysis_work_execution
		WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?`,
		fixture.work.ID, creatorID, operation.Attempt, jobID).Scan(ctx, &executionMode); err != nil {
		t.Fatalf("read immutable work execution mode: %v", err)
	}
	if executionMode != "staged" {
		t.Fatalf("work execution mode after root mode change = %q, want staged", executionMode)
	}

	projected, err := service.NewSourceLocationDetails(details).Read(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("project staged preparation: %v", err)
	}
	if projected.StagedArtifact.State != "preparation" || projected.StagedArtifact.ID != nil || projected.StagedArtifact.CreatorOperationID != nil {
		t.Fatalf("preparation projection = %#v, want identity-free preparation", projected.StagedArtifact)
	}
	if !projected.StagedArtifact.RequestedStepsKnown || len(projected.StagedArtifact.RequestedSteps) != 1 || projected.StagedArtifact.RequestedSteps[0] != string(persistence.SourceStepSHA256) {
		t.Fatalf("preparation requested steps = %#v, want known [sha256]", projected.StagedArtifact)
	}
	if len(preparing.Steps) != 1 || len(projected.Steps) != len(preparing.Steps) || projected.Steps[0].State != preparing.Steps[0].State {
		t.Fatalf("existing analysis step changed during projection: %+v", preparing.Steps)
	}

	artifactID := uuid.New()
	if _, err := fixture.database.NewRaw(`INSERT INTO source_analysis_artifact
		(id, work_id, relative_output_path, source_size_bytes, source_mtime, owner_operation_id,
		 owner_operation_attempt, owner_job_id, state, requested_steps, requested_steps_known)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, 'acquiring', ARRAY['sha256']::text[], true)`,
		artifactID, fixture.work.ID, "analysis/staging/root/work/artifact", fixture.work.SizeBytes,
		fixture.work.Mtime, creatorID, jobID).Exec(ctx); err != nil {
		t.Fatalf("insert acquiring artifact: %v", err)
	}
	for _, test := range []struct {
		state string
		want  string
	}{
		{state: "acquiring", want: "acquiring"},
		{state: "ready", want: "retained"},
		{state: "cleanup_eligible", want: "cleanup_eligible"},
	} {
		if _, err := fixture.database.NewRaw(`UPDATE source_analysis_artifact SET state=? WHERE id=?`, test.state, artifactID).Exec(ctx); err != nil {
			t.Fatalf("set artifact state %s: %v", test.state, err)
		}
		view, err := service.NewSourceLocationDetails(details).Read(ctx, fixture.root.ID, fixture.location.ID)
		if err != nil {
			t.Fatalf("read %s artifact: %v", test.state, err)
		}
		artifact := view.StagedArtifact
		if artifact.State != test.want || artifact.ID == nil || *artifact.ID != artifactID || artifact.CreatorOperationID == nil || *artifact.CreatorOperationID != creatorID || len(artifact.RequestedSteps) != 1 || artifact.RequestedSteps[0] != "sha256" || !artifact.RequestedStepsKnown {
			t.Fatalf("%s artifact projection = %#v, want state %q and its registry facts", test.state, artifact, test.want)
		}
	}

	cleanupError := "cleanup could not be completed"
	if _, err := fixture.database.NewRaw(`UPDATE source_analysis_artifact
		SET state='cleanup_failed', cleanup_error=?, cleanup_at=now() WHERE id=?`, cleanupError, artifactID).Exec(ctx); err != nil {
		t.Fatalf("mark artifact cleanup failed: %v", err)
	}
	failed, err := service.NewSourceLocationDetails(details).Read(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read failed artifact: %v", err)
	}
	if failed.StagedArtifact.State != "cleanup_failed" || failed.StagedArtifact.SafeError == nil || *failed.StagedArtifact.SafeError != cleanupError {
		t.Fatalf("failed artifact projection = %#v", failed.StagedArtifact)
	}

	// A changed root path no longer establishes this work as current. The detail
	// must not attribute its old artifact to the newly configured inventory.
	if _, err := fixture.database.NewRaw(`UPDATE source_root SET configured_path='/srv/analysis-start-new' WHERE id=?`, fixture.root.ID).Exec(ctx); err != nil {
		t.Fatalf("change root path: %v", err)
	}
	changed, err := service.NewSourceLocationDetails(details).Read(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read after root path change: %v", err)
	}
	if changed.StagedArtifact.State != "unknown" || changed.StagedArtifact.ID != nil {
		t.Fatalf("stale artifact projection after root path change = %#v", changed.StagedArtifact)
	}
}
