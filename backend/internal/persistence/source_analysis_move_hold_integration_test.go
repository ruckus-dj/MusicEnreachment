//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestToolsMoveEnqueueRefusesAnyActiveAnalysisHoldWithPostgreSQL proves the move
// boundary is global: an active analysis holds a managed installation, and a
// tools move whose own snapshot lists no files still refuses under the operation
// table lock before it inserts anything, because the move rewrites the global
// tools root those analyses pinned.
func TestToolsMoveEnqueueRefusesAnyActiveAnalysisHoldWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	if err := persistence.NewSettingsRepository(database).Set(ctx, "tools_directory", "/srv/tools-before"); err != nil {
		t.Fatal(err)
	}
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/move-hold")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "held")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)
	if err := enqueueAnalysis(t, ctx, inventory, analysisEnqueueOperation(root, location, installationID, nil), client); err != nil {
		t.Fatalf("enqueue the holding analysis: %v", err)
	}

	// The move snapshot lists no files at all; the global hold must still refuse.
	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-before","new_root":"/srv/tools-after","files":[]}`),
	}
	if err := setup.CreateToolsMoveOperationAndEnqueue(ctx, move, client,
		serviceOperationArgs{OperationID: move.ID}, nil); !errors.Is(err, persistence.ErrToolsInstallationHeldByAnalysis) {
		t.Fatalf("tools move with an empty file list and an active hold = %v, want ErrToolsInstallationHeldByAnalysis", err)
	}
	if move.RiverJobID != nil {
		t.Fatalf("refused move retained a River job id %d, want none before InsertTx", *move.RiverJobID)
	}
	if moves := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'move_tools_root'"); moves != 0 {
		t.Fatalf("move operations after the refusal = %d, want 0", moves)
	}
	if jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", serviceOperationArgs{}.Kind()); jobs != 0 {
		t.Fatalf("move River jobs after the refusal = %d, want 0", jobs)
	}
}
