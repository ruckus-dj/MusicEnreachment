//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceScanDeliveryFencesMutationsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/scan-delivery-fence")

	// The fixture enqueues the operation through the real repository method, so
	// the stored row carries the River job id the delivery fences compare
	// against. The snapshot is built from the JSON contract instead of the
	// typed producer so it compiles against both snapshot schema versions.
	snapshot, err := json.Marshal(map[string]any{
		"schema_version":  service.SourceScanSnapshotVersion,
		"source_root_id":  root.ID,
		"configured_path": root.ConfiguredPath,
		"scan_generation": root.ScanGeneration,
		"sha256_enabled":  true,
		"tools":           []any{},
	})
	if err != nil {
		t.Fatalf("encode scan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: "queued", Attempt: 1,
		InputSnapshot:      snapshot,
		TargetSourceRootID: &root.ID,
	}
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, operation, openScanEnqueueRiver(t, database),
		service.ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("enqueue scan operation: %v", err)
	}
	jobID := *operation.RiverJobID

	if err := inventory.StartSourceScanDelivery(ctx, operation.ID, operation.Attempt, jobID+1); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("start delivery with a different River job id = %v, want stale delivery", err)
	}
	if err := inventory.StartSourceScanDelivery(ctx, operation.ID, operation.Attempt, jobID); err != nil {
		t.Fatalf("start current delivery: %v", err)
	}

	initial := sourceCandidate("album/initial.flac", 1024, probeMtime())
	if err := inventory.AppendSourceScanCandidatesForDelivery(ctx, operation.ID, operation.Attempt, jobID, []persistence.SourceScanCandidateInput{initial}); err != nil {
		t.Fatalf("append current delivery candidate: %v", err)
	}

	wrongAttempt := operation.Attempt + 1
	additional := sourceCandidate("album/wrong-attempt.flac", 2048, probeMtime())
	if err := inventory.AppendSourceScanCandidatesForDelivery(ctx, operation.ID, wrongAttempt, jobID, []persistence.SourceScanCandidateInput{additional}); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("append with a different attempt = %v, want stale delivery", err)
	}
	if err := inventory.DeleteSourceScanCandidatesForDelivery(ctx, operation.ID, wrongAttempt, jobID); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("delete with a different attempt = %v, want stale delivery", err)
	}

	if _, err := database.NewRaw(`UPDATE operation SET state='failed',safe_error='test completed failure',finished_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatalf("record terminal delivery: %v", err)
	}
	if err := inventory.DeleteSourceScanCandidatesForDelivery(ctx, operation.ID, operation.Attempt, jobID); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("delete candidates for a terminal delivery = %v, want stale delivery", err)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 1)
}
