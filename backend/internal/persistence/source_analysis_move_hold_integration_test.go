//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

// TestToolsMoveEnqueueRefusesAnyActiveAnalysisHoldWithPostgreSQL proves the move
// boundary is global: an active analysis holds a managed installation, and a
// tools move whose own snapshot lists no files still refuses under the operation
// table lock before it inserts anything, because the move rewrites the global
// tools root those analyses pinned.
func runToolsMoveEnqueueRefusesAnyActiveAnalysisHold(t *testing.T, database *bun.DB, ctx context.Context, toolsRoot string) {
	t.Helper()
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	fixtureRoot := mustFixturePath(t, t.TempDir())
	toolsBefore := toolsRoot
	toolsAfter := filepath.Join(fixtureRoot, "tools-after")
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, filepath.Join(fixtureRoot, "move-hold"))
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "held")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)
	tool := normalizedToolSelection(t, ctx, database, installationID, "ffprobe")
	analysis := normalizedQueuedAnalysis(t, ctx, inventory, root, location, []persistence.SourceAnalysisToolSelection{tool})
	if err := enqueueNormalizedAnalysis(t, ctx, inventory, client, analysis); err != nil {
		t.Fatalf("enqueue the holding analysis: %v", err)
	}

	// The move snapshot lists no files at all; the global hold must still refuse.
	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: mustFixtureJSON(t, struct {
			SchemaVersion int      `json:"schema_version"`
			OldRoot       string   `json:"old_root"`
			NewRoot       string   `json:"new_root"`
			Files         []string `json:"files"`
		}{1, toolsBefore, toolsAfter, []string{}}),
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
	if err := inventory.SettleNormalizedSourceAnalysisOperation(ctx, analysis.ID, "failed", "failed", "retired move-hold fixture"); err != nil {
		t.Fatalf("retire fixture analysis %s: %v", analysis.ID, err)
	}
}
