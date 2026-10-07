//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestSourceAnalysisEnqueueRootExclusivityWithPostgreSQL pins the root
// exclusivity the enqueue decides under the operation table lock: an active
// analysis excludes another analysis and a scan start, a root edit that changes
// the path or the enabled state and a root deletion all refuse while it runs,
// while a display-name edit is accepted and a root deletion succeeds once the
// analysis is terminal.
func TestSourceAnalysisEnqueueRootExclusivityWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-exclusive")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "exclusive")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)

	active := analysisEnqueueOperation(root, location, installationID, nil)
	if err := enqueueAnalysis(t, ctx, inventory, active, client); err != nil {
		t.Fatalf("enqueue the first analysis: %v", err)
	}
	if err := enqueueAnalysis(t, ctx, inventory, analysisEnqueueOperation(root, location, installationID, nil), client); !errors.Is(err, persistence.ErrSourceRootActiveAnalysis) {
		t.Fatalf("second analysis = %v, want ErrSourceRootActiveAnalysis", err)
	}
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, scanEnqueueOperation(root, uuid.New()), client,
		service.ScanSourceJobArgs{OperationID: uuid.New()}, nil); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("scan start with an active analysis = %v, want ErrSourceRootActiveScan", err)
	}

	renamed := *root
	renamed.DisplayName = "Renamed"
	if err := inventory.UpdateSourceRoot(ctx, &renamed); err != nil {
		t.Fatalf("display-name edit with an active analysis = %v, want it accepted", err)
	}
	disabled := *root
	disabled.Enabled = false
	if err := inventory.UpdateSourceRoot(ctx, &disabled); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("disable with an active analysis = %v, want ErrSourceRootActiveScan", err)
	}
	moved := *root
	moved.ConfiguredPath = root.ConfiguredPath + "-moved"
	if err := inventory.UpdateSourceRoot(ctx, &moved); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("path change with an active analysis = %v, want ErrSourceRootActiveScan", err)
	}
	if err := deleteInventoryRoot(ctx, inventory, root.ID); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("delete with an active analysis = %v, want ErrSourceRootActiveScan", err)
	}
	if operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 1 {
		t.Fatalf("analysis operations after the refusals = %d, want 1", operations)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != 1 {
		t.Fatalf("analysis River jobs after the refusals = %d, want 1", jobs)
	}

	setOperationState(t, ctx, database, active.ID, "succeeded")
	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete after the active analysis finished: %v", err)
	}
}
