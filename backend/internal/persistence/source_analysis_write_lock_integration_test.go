//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisStageTransitionTakesOperationTableLockBeforeRowWithPostgreSQL
// pins the lock order of an analysis stage transition. A competing transaction
// holds the operation table lock and then locks the source root and the active
// operation row, the order a root deletion or edit uses. If the transition took
// its operation row lock before the table lock, its own UPDATE would need the
// table ROW EXCLUSIVE that the competitor's SHARE ROW EXCLUSIVE blocks, while
// the competitor waits for the operation row: a deadlock. The query barrier
// proves the transition reaches its table lock first, the competitor commits, and
// the transition finishes with the running stage and both holds kept.
func TestSourceAnalysisStageTransitionTakesOperationTableLockBeforeRowWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	operation := insertAnalysisOperation(t, ctx, fixture, &fixture.previousVariantID, "queued", service.SourceAnalysisStageQueued)
	operations := service.NewOperations(fixture.setup)

	err := runOperationLockRace(t, ctx, fixture.database, lockRootAndActiveOperation(fixture.root.ID),
		func(ctx context.Context) error {
			return operations.Running(ctx, operation.ID, service.SourceAnalysisStageProbing)
		})
	if err != nil {
		t.Fatalf("stage transition with a competing root mutation = %v, want no deadlock", err)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the transitioned operation: %v", err)
	}
	if stored.State != "running" || stored.Stage != service.SourceAnalysisStageProbing {
		t.Fatalf("operation after the transition = %s/%s, want running/%s", stored.State, stored.Stage, service.SourceAnalysisStageProbing)
	}
	if stored.AnalysisMediaVariantID == nil || *stored.AnalysisMediaVariantID != fixture.previousVariantID || stored.AnalysisInstallationID == nil {
		t.Fatalf("stage transition released a hold: %+v", stored)
	}
}

// TestSourceAnalysisFailTakesOperationTableLockBeforeRowWithPostgreSQL pins the
// lock order of the terminal failure. The same competing root-mutation ordering
// holds the table lock and the root; the failure reaches its table lock first and
// then releases both holds, so the two transactions cannot cycledeadlock. The
// committed failure keeps the previous variant linked.
func TestSourceAnalysisFailTakesOperationTableLockBeforeRowWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	operation := insertAnalysisOperation(t, ctx, fixture, &fixture.previousVariantID, "running", service.SourceAnalysisStageProbing)

	err := runOperationLockRace(t, ctx, fixture.database, lockRootAndActiveOperation(fixture.root.ID),
		func(ctx context.Context) error {
			return fixture.inventory.FailSourceAnalysisOperation(ctx, operation.ID, service.SourceAnalysisStageProbing, "The source file could not be analyzed. The previous result is unchanged.")
		})
	if err != nil {
		t.Fatalf("terminal failure with a competing root mutation = %v, want no deadlock", err)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the failed operation: %v", err)
	}
	if stored.State != "failed" || stored.AnalysisMediaVariantID != nil || stored.AnalysisInstallationID != nil {
		t.Fatalf("failed operation = %+v, want failed with both holds released", stored)
	}
	if !mediaVariantExists(t, ctx, fixture.database, fixture.previousVariantID) {
		t.Fatal("the failed analysis removed the previous variant")
	}
	linked, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read the location: %v", err)
	}
	if linked.MediaVariantID == nil || *linked.MediaVariantID != fixture.previousVariantID {
		t.Fatalf("previous variant link = %v, want the unchanged %s", linked.MediaVariantID, fixture.previousVariantID)
	}
}

// lockRootAndActiveOperation simulates the lock sequence a root mutation takes
// after its operation table lock: the root row, then the active operation row.
func lockRootAndActiveOperation(rootID uuid.UUID) func(context.Context, bun.Tx) error {
	return func(ctx context.Context, tx bun.Tx) error {
		var lockedRootID string
		if err := tx.NewRaw("SELECT id FROM source_root WHERE id = ? FOR UPDATE", rootID).Scan(ctx, &lockedRootID); err != nil {
			return err
		}
		var activeID string
		return tx.NewRaw("SELECT id FROM operation WHERE target_source_root_id = ? AND state IN ('queued', 'running') FOR UPDATE", rootID).Scan(ctx, &activeID)
	}
}
