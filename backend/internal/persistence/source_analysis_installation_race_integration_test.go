//go:build integration

package persistence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisEnqueueLocksPinnedInstallationWithPostgreSQL proves that
// admission takes its installation row lock after the root lock and holds it
// through insertion. A competing transaction's row lock makes admission block
// on the actual SELECT FOR SHARE statement; activation remains independently
// guarded by the package-selection coordination lock.
func runSourceAnalysisEnqueueLocksPinnedInstallation(t *testing.T, database *bun.DB) {
	t.Helper()
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, mustFixturePath(t, t.TempDir()))
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	selected := insertAnalysisInstallation(t, ctx, database, "selected-"+uuid.NewString())
	setActiveAnalysisFFmpeg(t, ctx, database, selected)

	tool := normalizedToolSelection(t, ctx, database, selected, "ffprobe")
	operation := normalizedQueuedAnalysis(t, ctx, inventory, root, location, []persistence.SourceAnalysisToolSelection{tool})
	jobsBefore := analysisJobs(t, ctx, database)
	err := runQueryRace(t, ctx, database,
		fmt.Sprintf("SELECT id FROM tool_installation WHERE id = '%s' FOR UPDATE", selected),
		"FOR SHARE",
		func(context.Context, bun.Tx) error { return nil },
		func(ctx context.Context) error {
			return enqueueNormalizedAnalysis(t, ctx, inventory, client, operation)
		})
	if err != nil {
		t.Fatalf("analysis admission after the installation metadata writer commits: %v", err)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != jobsBefore+1 {
		t.Fatalf("analysis River jobs after admission = %d, want %d", jobs, jobsBefore+1)
	}
}
