//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestRetainedSourceAnalysisReadmissionAfterDismissalWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/retained-readmission")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	installationID := uuid.New()
	now := time.Now().UTC()
	relativePath, err := tools.ManagedRelativePath(tools.PackageFFmpeg, "retained-release")
	if err != nil {
		t.Fatalf("resolve managed ffmpeg path: %v", err)
	}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: "retained-release", RelativePath: relativePath,
		State: "ready", ExecutableVersions: json.RawMessage(`{"ffprobe":"ffprobe version 7.1.2"}`),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert pinned installation: %v", err)
	}
	tool := persistence.SourceAnalysisToolSelection{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: installation.RelativePath, Executable: "ffprobe", Version: "7.1.2", VersionBanner: "ffprobe version 7.1.2"}
	fpcalcInstallation := insertAnalysisFPCalcFixture(t, ctx, database, "retained-fpcalc-release", "1.5.1")
	fpcalcTool := normalizedToolSelection(t, ctx, database, fpcalcInstallation.ID, "fpcalc")

	// A plain pending row has no durable admitted intent. Single-step admission
	// must reject it, while initial batch admission remains valid.
	sha := string(persistence.SourceStepSHA256)
	ordinary := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &sha, true, false, nil)
	if err := enqueueNormalizedAnalysis(t, ctx, repository, client, ordinary); err == nil {
		t.Fatal("single-step admission accepted a NULL-input ordinary pending step")
	}
	assertOperationHoldCounts(t, ctx, database, ordinary.ID, 0, 0)
	if jobs := analysisJobs(t, ctx, database); jobs != 0 {
		t.Fatalf("rejected ordinary admission created %d River jobs", jobs)
	}
	batch := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, []persistence.SourceAnalysisToolSelection{tool, fpcalcTool})
	if err := enqueueNormalizedAnalysis(t, ctx, repository, client, batch); err != nil {
		t.Fatalf("initial batch admission of ordinary pending work: %v", err)
	}
	if batch.RiverJobID == nil {
		t.Fatal("admitted original batch has no River job")
	}

	// Recover the original delivery so its unfinished steps become pending and
	// its canonical per-step projections survive dismissal of operation history.
	probe := string(persistence.SourceStepProbe)
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='running',stage='probing',started_at=now() WHERE id=?`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *batch.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverNormalizedSourceAnalysisDelivery(ctx, batch.ID, persistence.SourceAnalysisOperationDelivery{
		Attempt: batch.Attempt, JobID: *batch.RiverJobID,
	}, "The source analysis was interrupted."); err != nil {
		t.Fatalf("recover original batch delivery: %v", err)
	}
	var recoveredProbe persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&recoveredProbe).Where("work_id=? AND step='probe'", work.ID).Scan(ctx); err != nil {
		t.Fatalf("read recovered probe step: %v", err)
	}
	if recoveredProbe.State != "pending" || recoveredProbe.LastOperationID == nil || *recoveredProbe.LastOperationID != batch.ID || len(recoveredProbe.InputSnapshot) == 0 ||
		recoveredProbe.ExecutionOperationID != nil || recoveredProbe.ExecutionOperationAttempt != nil || recoveredProbe.ExecutionJobID != nil {
		t.Fatalf("recovery did not preserve pending probe intent without its execution fence: %+v", recoveredProbe)
	}
	assertOperationHoldCounts(t, ctx, database, batch.ID, 0, 0)
	retained := string(recoveredProbe.InputSnapshot)
	if _, err := database.ExecContext(ctx, `DELETE FROM operation WHERE id=?`, batch.ID); err != nil {
		t.Fatalf("dismiss old operation history: %v", err)
	}

	// A different pinned tool version must refuse without job or holds.
	changedTool := tool
	changedTool.Version = "7.1.1"
	changed := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &probe, true, false, []persistence.SourceAnalysisToolSelection{changedTool})
	jobsBefore := analysisJobs(t, ctx, database)
	if err := enqueueNormalizedAnalysis(t, ctx, repository, client, changed); err == nil {
		t.Fatal("readmission replaced retained pinned tool selection")
	}
	assertOperationHoldCounts(t, ctx, database, changed.ID, 0, 0)
	if jobs := analysisJobs(t, ctx, database); jobs != jobsBefore {
		t.Fatalf("mismatched readmission changed jobs %d -> %d", jobsBefore, jobs)
	}

	// Removing the retained input makes it ordinary again and must still reject.
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET input_snapshot=NULL,last_operation_id=NULL WHERE work_id=? AND step='probe'`, work.ID); err != nil {
		t.Fatal(err)
	}
	noIntent := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &probe, true, false, []persistence.SourceAnalysisToolSelection{tool})
	if err := enqueueNormalizedAnalysis(t, ctx, repository, client, noIntent); err == nil {
		t.Fatal("ordinary NULL-input probe pending step was readmitted")
	}
	assertOperationHoldCounts(t, ctx, database, noIntent.ID, 0, 0)

	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET input_snapshot=?::jsonb,last_operation_id=? WHERE work_id=? AND step='probe'`, retained, batch.ID, work.ID); err != nil {
		t.Fatal(err)
	}
	readmission := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &probe, true, false, []persistence.SourceAnalysisToolSelection{tool})
	if err := enqueueNormalizedAnalysis(t, ctx, repository, client, readmission); err != nil {
		t.Fatalf("readmit retained probe after dismissal: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, readmission.ID, 1, 1)
	var probeState, shaState, fingerprintState string
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='probe'`, work.ID).Scan(ctx, &probeState); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='sha256'`, work.ID).Scan(ctx, &shaState); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &fingerprintState); err != nil {
		t.Fatal(err)
	}
	if probeState != "queued" || shaState != "pending" || fingerprintState != "pending" {
		t.Fatalf("step states after readmission probe=%s sha=%s fingerprint=%s", probeState, shaState, fingerprintState)
	}
	var cacheOnly bool
	if err := database.NewRaw(`SELECT (input_snapshot->>'cache_only_reuse')::boolean FROM operation WHERE id=?`, readmission.ID).Scan(ctx, &cacheOnly); err != nil {
		t.Fatal(err)
	}
	if cacheOnly {
		t.Fatal("ordinary retained probe unexpectedly became cache-only")
	}
}
