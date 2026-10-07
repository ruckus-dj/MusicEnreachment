//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisEnqueueFencesActiveInstallationUnderLockWithPostgreSQL
// proves the start's linearization against activation. The competing
// transaction takes the same active-installation advisory lock the enqueue uses
// and commits a different active setting under it; the enqueue, proven by the
// query barrier to have reached that advisory lock, must then refuse the start
// rather than queue an analysis pinned to an installation the operator no longer
// sees. An activation that commits after the enqueue cannot touch the queued
// snapshot, which is covered by the immutable-snapshot tests.
func TestSourceAnalysisEnqueueFencesActiveInstallationUnderLockWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-activation-race")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	selected := insertAnalysisInstallation(t, ctx, database, "selected")
	setActiveAnalysisFFmpeg(t, ctx, database, selected)
	replacement := insertAnalysisInstallation(t, ctx, database, "replacement")

	operation := analysisEnqueueOperation(root, location, selected, nil)
	err := runQueryRace(t, ctx, database,
		"SELECT pg_advisory_xact_lock(hashtext('active-installation:ffmpeg'))",
		"active-installation:ffmpeg",
		func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.NewInsert().Model(&persistence.AppSetting{
				Name: settings.ActiveFFmpegInstallationKey, Value: replacement.String(),
			}).On("CONFLICT (setting_name) DO UPDATE").
				Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx)
			return err
		},
		func(ctx context.Context) error { return enqueueAnalysis(t, ctx, inventory, operation, client) })
	if !errors.Is(err, persistence.ErrSourceAnalysisInstallationChanged) {
		t.Fatalf("analysis start racing an activation = %v, want ErrSourceAnalysisInstallationChanged", err)
	}
	assertNoAnalysisStart(t, ctx, database)
}
