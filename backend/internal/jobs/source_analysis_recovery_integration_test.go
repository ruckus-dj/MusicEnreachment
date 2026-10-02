//go:build integration

package jobs

import (
	"context"
	"encoding/json"
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
// both read holds released and publishes no variant; an analysis whose apply
// really committed is already succeeded, so recovery leaves it untouched and a
// duplicate worker delivery re-reads nothing; and an analysis whose real River
// delivery is still scheduled is preserved.
func TestSourceAnalysisStartupRecoveryPostgreSQL(t *testing.T) {
	t.Run("interrupted before commit", func(t *testing.T) {
		fixture := newAnalysisDispatchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		operation := fixture.createOrphanAnalysis(t, ctx, "running", service.SourceAnalysisStageProbing)
		probesBefore := scanDispatchProbeCount(t, fixture.probeLog)

		reconcileAnalysisRecovery(t, ctx, fixture)

		assertOperationStage(t, ctx, fixture.setup, operation.ID, "failed", service.SourceAnalysisStageProbing)
		requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, operation.ID), analysisSafeInterrupted)
		requireAnalysisHolds(t, fixture.readOperation(t, ctx, operation.ID), nil, nil)
		if variants := fixture.countVariants(t, ctx); variants != 0 {
			t.Fatalf("media variants after the recovery = %d, want none committed", variants)
		}
		if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesBefore {
			t.Fatalf("probes during the recovery = %d, want the unchanged %d", probes, probesBefore)
		}
	})

	t.Run("interrupted after commit", func(t *testing.T) {
		fixture := newAnalysisDispatchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		operation := fixture.createOrphanAnalysis(t, ctx, "running", service.SourceAnalysisStageApplying)
		// The real apply commits the variant, the location link and the succeeded
		// state in one transaction; only the process and its wake-up are missing.
		committed := fixture.applyRunningAnalysis(t, ctx, operation)
		probesBefore := scanDispatchProbeCount(t, fixture.probeLog)

		reconcileAnalysisRecovery(t, ctx, fixture)

		assertOperationStage(t, ctx, fixture.setup, operation.ID, "succeeded", service.SourceAnalysisStageApplying)
		requireAnalysisHolds(t, fixture.readOperation(t, ctx, operation.ID), nil, nil)
		if fixture.requireLinkedVariant(t, ctx) != committed {
			t.Fatal("the committed result was not kept linked")
		}
		if variants := fixture.countVariants(t, ctx); variants != 1 {
			t.Fatalf("media variants after the recovery = %d, want the committed one", variants)
		}
		// A duplicate delivery of the already succeeded operation reads nothing.
		awaitRiverCompletion(t, ctx, fixture.events, fixture.deliver(t, ctx, operation.ID))
		if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesBefore {
			t.Fatalf("probes after the duplicate delivery = %d, want none", probes)
		}
		if variants := fixture.countVariants(t, ctx); variants != 1 {
			t.Fatalf("media variants after the duplicate delivery = %d, want the committed one", variants)
		}
	})

	t.Run("live delivery preserved", func(t *testing.T) {
		fixture := newAnalysisDispatchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		operation := fixture.createOrphanAnalysis(t, ctx, "queued", service.SourceAnalysisStageQueued)
		// A real River row in a live (scheduled) state, inserted through the real
		// client, is what the production liveness check reads.
		jobID := fixture.scheduleRiverDelivery(t, ctx, operation.ID)
		if _, err := fixture.database.ExecContext(ctx, "UPDATE operation SET river_job_id = ? WHERE id = ?", jobID, operation.ID); err != nil {
			t.Fatalf("attach the live delivery: %v", err)
		}

		reconcileAnalysisRecovery(t, ctx, fixture)

		stored := fixture.readOperation(t, ctx, operation.ID)
		if stored.State != "queued" || stored.AnalysisInstallationID == nil || stored.AnalysisMediaVariantID != nil {
			t.Fatalf("live analysis was touched: %+v", stored)
		}
	})
}

// createOrphanAnalysis inserts one analysis operation with both read holds and
// the immutable snapshot of the fixture's location, without a River job, which
// is the state of an analysis whose delivery is gone.
func (fixture *analysisDispatchFixture) createOrphanAnalysis(t *testing.T, ctx context.Context, state, stage string) *persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(persistence.SourceAnalysisSnapshot{
		SchemaVersion:          persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:           fixture.root.ID,
		SourceLocationID:       fixture.track.ID,
		ConfiguredPath:         fixture.root.ConfiguredPath,
		InventoryPath:          fixture.root.ConfiguredPath,
		RelativePath:           fixture.track.RelativePath,
		SizeBytes:              fixture.track.SizeBytes,
		Mtime:                  fixture.track.Mtime,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: fixture.installationID,
	})
	if err != nil {
		t.Fatalf("marshal the orphan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: state, Stage: stage, Attempt: 1,
		InputSnapshot:          snapshot,
		TargetSourceRootID:     &fixture.root.ID,
		TargetSourceLocationID: &fixture.track.ID,
		AnalysisInstallationID: &fixture.installationID,
	}
	if _, err := fixture.database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert the orphan analysis: %v", err)
	}
	return operation
}

// applyRunningAnalysis commits the prepared result of the running operation
// through the real apply, then returns the variant it linked to the location.
func (fixture *analysisDispatchFixture) applyRunningAnalysis(t *testing.T, ctx context.Context, operation *persistence.Operation) uuid.UUID {
	t.Helper()
	apply := persistence.SourceAnalysisApply{
		OperationID: operation.ID, RelativePath: fixture.track.RelativePath,
		SizeBytes: fixture.track.SizeBytes, Mtime: fixture.track.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion:        "ffprobe version " + analysisDispatchRelease,
		FFProbeJSON:           json.RawMessage(`{"format":{},"streams":[]}`),
		ObservedTags:          json.RawMessage(`{}`),
		InspectedAt:           time.Now().UTC(),
	}
	if _, err := fixture.inventory.ApplyAnalysisResult(ctx, apply); err != nil {
		t.Fatalf("apply the running analysis: %v", err)
	}
	return fixture.requireLinkedVariant(t, ctx)
}

// scheduleRiverDelivery inserts a real River job one hour out, so the job row is
// in the live scheduled state and no worker consumes it during the test.
func (fixture *analysisDispatchFixture) scheduleRiverDelivery(t *testing.T, ctx context.Context, operationID uuid.UUID) int64 {
	t.Helper()
	tx, err := fixture.database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin the scheduled delivery: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := fixture.client.InsertTx(ctx, tx, service.SourceAnalysisJobArgs{OperationID: operationID},
		&river.InsertOpts{Queue: service.SourceAnalysisQueue, ScheduledAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("insert the scheduled delivery: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit the scheduled delivery: %v", err)
	}
	return inserted.Job.ID
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
