//go:build integration

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisStartupRecoveryPostgreSQL drives the analysis-specific
// startup recovery against real PostgreSQL and the production River liveness
// check. An orphaned analysis whose result never committed fails retryably with
// selected unfinished steps returned to pending and their delivery fences
// cleared; an analysis whose normal admission has a live River delivery, work
// selection, and tool hold is preserved.
func TestSourceAnalysisStartupRecoveryPostgreSQL(t *testing.T) {
	t.Parallel()
	t.Run("interrupted before commit", func(t *testing.T) {
		fixture := newAnalysisDispatchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		operation := fixture.createOrphanAnalysis(t, ctx, "running", service.SourceAnalysisStageProbing)
		admitted := fixture.readOperation(t, ctx, operation.ID)
		probesBefore := scanDispatchProbeCount(t, fixture.probeLog)

		reconcileAnalysisRecovery(t, ctx, fixture)

		assertOperationStage(t, ctx, fixture.setup, operation.ID, "failed", "recovered")
		recovered := fixture.readOperation(t, ctx, operation.ID)
		requireScanDispatchSafeError(t, recovered, analysisSafeInterrupted)
		if !bytes.Equal(recovered.InputSnapshot, admitted.InputSnapshot) {
			t.Fatal("recovery changed the admitted input snapshot")
		}
		requireAnalysisHolds(t, ctx, fixture, operation.ID, nil, nil)
		if variants := fixture.countVariants(t, ctx); variants != 0 {
			t.Fatalf("media variants after the recovery = %d, want none committed", variants)
		}
		snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
		if err != nil {
			t.Fatalf("decode recovered normalized snapshot: %v", err)
		}
		var steps []persistence.SourceAnalysisStep
		if err := fixture.database.NewSelect().Model(&steps).Where("work_id = ?", snapshot.WorkIDs[0]).Order("step").Scan(ctx); err != nil {
			t.Fatalf("read recovered steps: %v", err)
		}
		states := make(map[string]persistence.SourceAnalysisStep, len(steps))
		for _, step := range steps {
			states[step.Step] = step
		}
		sha := states[string(persistence.SourceStepSHA256)]
		if sha.State != "pending" || sha.ExecutionOperationID != nil || sha.ExecutionOperationAttempt != nil || sha.ExecutionJobID != nil {
			t.Fatalf("interrupted SHA step after recovery = %+v, want pending with its delivery fence cleared", sha)
		}
		probe := states[string(persistence.SourceStepProbe)]
		if probe.State != "pending" || probe.ExecutionOperationID != nil || probe.ExecutionOperationAttempt != nil || probe.ExecutionJobID != nil {
			t.Fatalf("interrupted probe step after recovery = %+v, want pending with its delivery fence cleared", probe)
		}
		fingerprint := states[string(persistence.SourceStepFingerprint)]
		if fingerprint.State != "failed" || fingerprint.SafeError == nil || *fingerprint.SafeError != "previous fingerprint failure" {
			t.Fatalf("failed fingerprint sibling changed during recovery: %+v", fingerprint)
		}
		if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesBefore {
			t.Fatalf("probes during the recovery = %d, want the unchanged %d", probes, probesBefore)
		}
	})

	t.Run("live delivery preserved", func(t *testing.T) {
		fixture := newAnalysisDispatchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		operation := fixture.createOrphanAnalysis(t, ctx, "queued", service.SourceAnalysisStageQueued)

		reconcileAnalysisRecovery(t, ctx, fixture)

		stored := fixture.readOperation(t, ctx, operation.ID)
		if stored.State != "queued" || stored.RiverJobID == nil || !stored.ToolsReadRequired || stored.TargetSourceRootID == nil || *stored.TargetSourceRootID != fixture.root.ID {
			t.Fatalf("live analysis was touched: %+v", stored)
		}
		snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(stored.InputSnapshot)
		if err != nil {
			t.Fatalf("decode live normalized snapshot: %v", err)
		}
		if len(snapshot.WorkIDs) != 1 || len(snapshot.Tools) != 0 {
			t.Fatalf("live durable admission intent = %+v, want selected work and no runtime tools", snapshot)
		}
		if len(operation.SourceAnalysisTools) != 1 || operation.SourceAnalysisTools[0].InstallationID != fixture.installationID ||
			operation.SourceAnalysisTools[0].PackageKind != "ffmpeg" || operation.SourceAnalysisTools[0].Executable != "ffprobe" ||
			operation.SourceAnalysisTools[0].RelativePath != filepath.Join("ffmpeg", analysisDispatchRelease) ||
			operation.SourceAnalysisTools[0].Version != analysisDispatchRelease ||
			operation.SourceAnalysisTools[0].VersionBanner != "ffprobe version "+analysisDispatchRelease {
			t.Fatalf("live runtime admission selectors = %+v, want the selected ffprobe installation", operation.SourceAnalysisTools)
		}
		requireAnalysisHolds(t, ctx, fixture, stored.ID, []uuid.UUID{fixture.installationID}, snapshot.WorkIDs)
		var selected persistence.SourceAnalysisStep
		if err := fixture.database.NewSelect().Model(&selected).Where("work_id = ? AND step = ?", snapshot.WorkIDs[0], string(persistence.SourceStepProbe)).Scan(ctx); err != nil {
			t.Fatalf("read the live probe execution selector: %v", err)
		}
		if selected.State != "queued" || selected.ExecutionOperationID == nil || *selected.ExecutionOperationID != stored.ID || selected.ExecutionOperationAttempt == nil || *selected.ExecutionOperationAttempt != stored.Attempt || selected.ExecutionJobID == nil || *selected.ExecutionJobID != *stored.RiverJobID {
			t.Fatalf("live probe execution selector was changed: %+v", selected)
		}
	})
}

// createOrphanAnalysis admits a real operation against normalized work with a
// future River delivery, then optionally simulates a process interruption after
// the selected steps have started. Normal admission writes the work selection,
// managed-tool hold, operation, River job, and step execution fences atomically.
func (fixture *analysisDispatchFixture) createOrphanAnalysis(t *testing.T, ctx context.Context, state, stage string) *persistence.Operation {
	t.Helper()
	work := fixture.work
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='pending',safe_error=NULL,
		input_snapshot=NULL,execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL
		WHERE work_id=? AND step IN ('sha256','probe')`, work.ID); err != nil {
		t.Fatalf("prepare pending recovery steps: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='failed',safe_error=?,input_snapshot=NULL,
		execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL WHERE work_id=? AND step='fingerprint'`,
		"previous fingerprint failure", work.ID); err != nil {
		t.Fatalf("prepare historical fingerprint failure: %v", err)
	}
	shaEnabled, rerun, cacheOnly := true, false, false
	probe := persistence.SourceAnalysisToolSelection{
		PackageKind: "ffmpeg", InstallationID: fixture.installationID,
		RelativePath: filepath.Join("ffmpeg", analysisDispatchRelease), Executable: "ffprobe",
		Version: analysisDispatchRelease, VersionBanner: "ffprobe version " + analysisDispatchRelease,
	}
	snapshot, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeBatch, WorkIDs: []uuid.UUID{work.ID},
		SelectedSteps: []persistence.SourceAnalysisStepSelection{
			{WorkID: work.ID, Step: persistence.SourceStepSHA256},
			{WorkID: work.ID, Step: persistence.SourceStepProbe},
		},
		SHA256Enabled: &shaEnabled, RerunTarget: &rerun, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: []persistence.SourceAnalysisToolSelection{probe},
	})
	if err != nil {
		t.Fatalf("marshal the orphan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued, Attempt: 1,
		SourceAnalysisMode:  persistence.SourceAnalysisModeBatch,
		InputSnapshot:       snapshot,
		TargetSourceRootID:  &fixture.root.ID,
		ToolsReadRequired:   true,
		SourceAnalysisTools: []persistence.SourceAnalysisToolSelection{probe},
	}
	if err := fixture.inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, fixture.client,
		service.SourceAnalysisJobArgs{OperationID: operation.ID},
		&river.InsertOpts{Queue: service.SourceAnalysisQueue, ScheduledAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("admit normalized interrupted analysis: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("normalized analysis admission has no River job")
	}
	if state == "running" {
		if _, err := fixture.database.ExecContext(ctx, "UPDATE operation SET state = ?, stage = ? WHERE id = ?", state, stage, operation.ID); err != nil {
			t.Fatalf("mark normalized operation interrupted: %v", err)
		}
		if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_step SET state='running',step_attempt=1
			WHERE work_id=? AND step IN ('sha256','probe') AND execution_operation_id=? AND execution_operation_attempt=? AND execution_job_id=?`,
			work.ID, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
			t.Fatalf("mark normalized steps interrupted: %v", err)
		}
		var runningSteps int
		if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND state='running'
			AND execution_operation_id=? AND execution_operation_attempt=? AND execution_job_id=?`,
			work.ID, operation.ID, operation.Attempt, *operation.RiverJobID).Scan(ctx, &runningSteps); err != nil {
			t.Fatalf("verify interrupted execution selectors: %v", err)
		}
		if runningSteps != 2 {
			t.Fatalf("interrupted steps with the old delivery fence = %d, want 2", runningSteps)
		}
		if _, err := fixture.database.ExecContext(ctx, "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = ?", *operation.RiverJobID); err != nil {
			t.Fatalf("mark interrupted River delivery terminal: %v", err)
		}
	}
	return operation
}

// reconcileAnalysisRecovery runs the production startup recovery with the exact
// production River liveness check the composition root uses.
func reconcileAnalysisRecovery(t *testing.T, ctx context.Context, fixture analysisDispatchFixture) {
	t.Helper()
	if err := ReconcileInterruptedOperations(ctx, fixture.setup, fixture.operations,
		fixture.setup.RiverJobLiveness, fixture.registry); err != nil {
		t.Fatalf("reconcile interrupted operations: %v", err)
	}
}
