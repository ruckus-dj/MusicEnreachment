//go:build integration

package persistence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisEnqueueLocksPinnedInstallationWithPostgreSQL proves that
// admission takes its installation row lock after the root lock and holds it
// through insertion. A competing transaction's row lock makes admission block
// on the actual SELECT FOR SHARE statement; activation remains independently
// guarded by the package-selection coordination lock.
func TestSourceAnalysisEnqueueLocksPinnedInstallationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-activation-race")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	selected := insertAnalysisInstallation(t, ctx, database, "selected")
	setActiveAnalysisFFmpeg(t, ctx, database, selected)

	tool := normalizedToolSelection(t, ctx, database, selected, "ffprobe")
	operation := normalizedQueuedAnalysis(t, ctx, inventory, root, location, []persistence.SourceAnalysisToolSelection{tool})
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
	if jobs := analysisJobs(t, ctx, database); jobs != 1 {
		t.Fatalf("analysis River jobs after admission = %d, want 1", jobs)
	}
}
