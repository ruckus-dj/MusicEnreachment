//go:build integration

package persistence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisStageTransitionSerializesOnItsOperationRowWithPostgreSQL
// proves a stage transition waits for the operation row it updates, without
// requiring a table-wide lock that would serialize unrelated operations.
func TestSourceAnalysisStageTransitionSerializesOnItsOperationRowWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, operation, previousVariantID := newWriteLockAnalysisFixture(t, ctx)
	operations := service.NewOperations(persistence.NewSetupManagerRepository(fixture.database))

	err := runQueryRace(t, ctx, fixture.database, operationRowLockQuery(operation.ID), "FROM operation",
		func(context.Context, bun.Tx) error { return nil },
		func(ctx context.Context) error {
			return operations.Running(ctx, operation.ID, service.SourceAnalysisStageProbing)
		})
	if err != nil {
		t.Fatalf("stage transition while its operation row is locked = %v", err)
	}
	stored, err := persistence.NewSetupManagerRepository(fixture.database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the transitioned operation: %v", err)
	}
	if stored.State != "running" || stored.Stage != service.SourceAnalysisStageProbing {
		t.Fatalf("operation after the transition = %s/%s, want running/%s", stored.State, stored.Stage, service.SourceAnalysisStageProbing)
	}
	assertOperationHoldCounts(t, ctx, fixture.database, operation.ID, 1, 1)
	if !mediaVariantExists(t, ctx, fixture.database, previousVariantID) {
		t.Fatal("the stage transition removed the previous variant")
	}
}

// TestSourceAnalysisFailureSerializesOnItsOperationRowWithPostgreSQL proves a
// normalized terminal failure waits for its own operation row before releasing
// its holds. Other operations remain free to progress independently.
func TestSourceAnalysisFailureSerializesOnItsOperationRowWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, operation, previousVariantID := newWriteLockAnalysisFixture(t, ctx)
	if err := service.NewOperations(persistence.NewSetupManagerRepository(fixture.database)).Running(ctx, operation.ID, service.SourceAnalysisStageProbing); err != nil {
		t.Fatalf("start normalized source analysis: %v", err)
	}

	err := runQueryRace(t, ctx, fixture.database, operationRowLockQuery(operation.ID), "FROM operation",
		func(context.Context, bun.Tx) error { return nil },
		func(ctx context.Context) error {
			return fixture.repository.SettleNormalizedSourceAnalysisOperation(ctx, operation.ID, "failed", service.SourceAnalysisStageProbing, "The source file could not be analyzed. The previous result is unchanged.")
		})
	if err != nil {
		t.Fatalf("terminal failure while its operation row is locked = %v", err)
	}
	stored, err := persistence.NewSetupManagerRepository(fixture.database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the failed operation: %v", err)
	}
	if stored.State != "failed" {
		t.Fatalf("failed operation = %+v, want failed", stored)
	}
	assertOperationHoldCounts(t, ctx, fixture.database, operation.ID, 0, 0)
	if !mediaVariantExists(t, ctx, fixture.database, previousVariantID) {
		t.Fatal("the failed analysis removed the previous variant")
	}
	linked, err := fixture.repository.GetSourceLocation(ctx, fixture.root.ID, fixture.location.ID)
	if err != nil {
		t.Fatalf("read the location: %v", err)
	}
	if linked.MediaVariantID == nil || *linked.MediaVariantID != previousVariantID {
		t.Fatalf("previous variant link = %v, want the unchanged %s", linked.MediaVariantID, previousVariantID)
	}
}

func newWriteLockAnalysisFixture(t *testing.T, ctx context.Context) (normalizedAnalysisFixture, *persistence.Operation, uuid.UUID) {
	t.Helper()
	fixture := newNormalizedWorkOperation(t, "/srv/normalized-analysis-write-lock", false,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepProbe, State: "pending"}})
	previousVariantID := insertMediaVariantRow(t, ctx, fixture.database, fixture.location.SizeBytes, uuid.New())
	linkLocationVariant(t, ctx, fixture.database, fixture.location.ID, previousVariantID)
	tool := insertVerifiedAnalysisTool(t, ctx, fixture.database, "ffmpeg", "ffprobe", "7.1", "ffprobe version 7.1")
	operation := fixture.batchOperation(t, tool)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit normalized source analysis: %v", err)
	}
	return fixture, operation, previousVariantID
}

// operationRowLockQuery locks only the operation under test. UUIDs are generated
// by the fixture, so embedding this value in the test-only SQL is safe.
func operationRowLockQuery(operationID uuid.UUID) string {
	return fmt.Sprintf("SELECT * FROM operation WHERE id = '%s' FOR UPDATE", operationID)
}
