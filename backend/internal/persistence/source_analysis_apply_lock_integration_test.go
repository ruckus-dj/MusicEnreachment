//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

// analysisApplyForRunningFixture builds the prepared result of the fixture's
// running analysis.
func analysisApplyForRunningFixture(t *testing.T, fixture analysisRetryFixture, operation *persistence.Operation) persistence.SourceAnalysisApply {
	t.Helper()
	return persistence.SourceAnalysisApply{
		OperationID: operation.ID, RelativePath: fixture.location.RelativePath,
		SizeBytes: fixture.location.SizeBytes, Mtime: fixture.location.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.4",
		FFProbeJSON: json.RawMessage(`{"format":{},"streams":[]}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(),
	}
}

// TestSourceAnalysisApplyTakesOperationTableLockBeforeRootWithPostgreSQL pins
// the lock order of the analysis apply. A competing transaction holds the
// operation table lock and then the source root, exactly the order a root
// deletion or edit takes before it locks the active operation row. If the apply
// took its operation row lock before the table lock, the two transactions would
// deadlock (apply holds the operation row and waits for the root the competitor
// holds, while the competitor waits for the operation row). The query barrier
// proves the apply reaches its table lock, the competitor commits, and the apply
// finishes with the allowed outcome: the result is committed.
func TestSourceAnalysisApplyTakesOperationTableLockBeforeRootWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	operation := insertAnalysisOperation(t, ctx, fixture, &fixture.previousVariantID, "running", "probing")
	apply := analysisApplyForRunningFixture(t, fixture, operation)

	err := runOperationLockRace(t, ctx, fixture.database,
		func(ctx context.Context, tx bun.Tx) error {
			var rootID string
			if err := tx.NewRaw("SELECT id FROM source_root WHERE id = ? FOR UPDATE", fixture.root.ID).Scan(ctx, &rootID); err != nil {
				return err
			}
			var activeID string
			return tx.NewRaw("SELECT id FROM operation WHERE target_source_root_id = ? AND state IN ('queued', 'running') FOR UPDATE", fixture.root.ID).Scan(ctx, &activeID)
		},
		func(ctx context.Context) error {
			_, err := fixture.inventory.ApplyAnalysisResult(ctx, apply)
			return err
		})
	if err != nil {
		t.Fatalf("apply with a competing root mutation = %v, want no deadlock", err)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the applied operation: %v", err)
	}
	if stored.State != "succeeded" || stored.AnalysisMediaVariantID != nil || stored.AnalysisInstallationID != nil {
		t.Fatalf("applied operation = %+v, want succeeded with both holds released", stored)
	}
	linked, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read the applied location: %v", err)
	}
	if linked.MediaVariantID == nil || *linked.MediaVariantID == fixture.previousVariantID {
		t.Fatalf("location link = %v, want the new variant", linked.MediaVariantID)
	}
	if mediaVariantExists(t, ctx, fixture.database, fixture.previousVariantID) {
		t.Fatal("the superseded previous variant was not orphaned")
	}
}

// TestSourceAnalysisApplyFencesRootChangedUnderLockWithPostgreSQL proves the
// ordering fix does not weaken the fence: a competing writer changes the root
// inventory under the same lock order, then commits; the apply reaches its table
// lock, observes the changed root and refuses cleanly with ErrSourceAnalysisStale
// instead of deadlocking or publishing the stale result.
func TestSourceAnalysisApplyFencesRootChangedUnderLockWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	operation := insertAnalysisOperation(t, ctx, fixture, &fixture.previousVariantID, "running", "probing")
	apply := analysisApplyForRunningFixture(t, fixture, operation)

	err := runOperationLockRace(t, ctx, fixture.database,
		func(ctx context.Context, tx bun.Tx) error {
			var rootID string
			if err := tx.NewRaw("SELECT id FROM source_root WHERE id = ? FOR UPDATE", fixture.root.ID).Scan(ctx, &rootID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE source_root SET inventory_path = inventory_path || '-moved' WHERE id = ?", fixture.root.ID); err != nil {
				return err
			}
			var activeID string
			return tx.NewRaw("SELECT id FROM operation WHERE target_source_root_id = ? AND state IN ('queued', 'running') FOR UPDATE", fixture.root.ID).Scan(ctx, &activeID)
		},
		func(ctx context.Context) error {
			_, err := fixture.inventory.ApplyAnalysisResult(ctx, apply)
			return err
		})
	if !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("apply against a root changed under the lock = %v, want %v", err, persistence.ErrSourceAnalysisStale)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the refused operation: %v", err)
	}
	if stored.State != "running" || stored.AnalysisMediaVariantID == nil || *stored.AnalysisMediaVariantID != fixture.previousVariantID ||
		stored.AnalysisInstallationID == nil {
		t.Fatalf("refused operation = %+v, want running with both holds intact", stored)
	}
	if countMediaVariants(t, ctx, fixture.database) != 1 {
		t.Fatalf("variants after the refused apply = %d, want the previous one alone", countMediaVariants(t, ctx, fixture.database))
	}
}
