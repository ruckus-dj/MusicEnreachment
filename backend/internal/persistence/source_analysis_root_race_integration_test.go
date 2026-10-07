//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type analysisRaceFixture struct {
	database       *bun.DB
	inventory      *persistence.SourceInventoryRepository
	client         persistence.RiverInserter
	root           *persistence.SourceRoot
	location       persistence.SourceLocation
	work           *persistence.SourceAnalysisWork
	installationID uuid.UUID
}

func newAnalysisRaceFixture(t *testing.T, path string) analysisRaceFixture {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, path)
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, inventory, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"})
	installationID := insertAnalysisInstallation(t, ctx, database, "root-race")
	return analysisRaceFixture{
		database: database, inventory: inventory, client: openScanEnqueueRiver(t, database),
		root: root, location: location, work: work, installationID: installationID,
	}
}

// TestSourceAnalysisStartRacesRootMutationsWithPostgreSQL runs the analysis
// start against a competing transaction that reaches the operation table lock
// first and commits a conflicting state under it: another analysis of the root
// and a scan of the root are active operations, an edit moves the configured
// path and a deletion removes the root. Each action reaches its lock (proven by
// the query barrier) before the competing transaction commits, so the refusal
// is decided at the transaction boundary, not by scheduling.
func TestSourceAnalysisStartRacesRootMutationsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	run := func(t *testing.T, path string, compete func(context.Context, bun.Tx, analysisRaceFixture) error, want error) {
		t.Helper()
		fixture := newAnalysisRaceFixture(t, path)
		operation := normalizedOperation(t, fixture.root, fixture.location, fixture.work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
		holdRoot := fmt.Sprintf("SELECT id FROM source_root WHERE id = '%s' FOR UPDATE", fixture.root.ID)
		err := runQueryRace(t, ctx, fixture.database, holdRoot, "FOR UPDATE",
			func(ctx context.Context, tx bun.Tx) error { return compete(ctx, tx, fixture) },
			func(ctx context.Context) error {
				return enqueueNormalizedAnalysis(t, ctx, fixture.inventory, fixture.client, operation)
			})
		if !errors.Is(err, want) {
			t.Fatalf("analysis start racing the competing mutation = %v, want %v", err, want)
		}
		if jobs := analysisJobs(t, ctx, fixture.database); jobs != 0 {
			t.Fatalf("analysis River jobs after the refusal = %d, want 0", jobs)
		}
	}

	t.Run("active_analysis", func(t *testing.T) {
		run(t, "/srv/race-analysis", func(ctx context.Context, tx bun.Tx, f analysisRaceFixture) error {
			competing := normalizedOperation(t, f.root, f.location, f.work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
			if err := insertQueuedOperation(ctx, tx, competing); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO operation_source_work_hold (operation_id, work_id) VALUES (?, ?)", competing.ID, f.work.ID)
			return err
		}, persistence.ErrSourceRootActiveAnalysis)
	})

	t.Run("active_scan", func(t *testing.T) {
		run(t, "/srv/race-scan", func(ctx context.Context, tx bun.Tx, f analysisRaceFixture) error {
			return insertQueuedOperation(ctx, tx, &persistence.Operation{
				ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
				InputSnapshot: json.RawMessage(`{}`), TargetSourceRootID: &f.root.ID,
			})
		}, persistence.ErrSourceRootActiveScan)
	})

	t.Run("path_edit", func(t *testing.T) {
		run(t, "/srv/race-edit", func(ctx context.Context, tx bun.Tx, f analysisRaceFixture) error {
			_, err := tx.NewUpdate().Model((*persistence.SourceRoot)(nil)).
				Set("configured_path = ?", f.root.ConfiguredPath+"-moved").Set("updated_at = now()").
				Where("id = ?", f.root.ID).Exec(ctx)
			return err
		}, persistence.ErrSourceAnalysisStale)
	})

	t.Run("root_delete", func(t *testing.T) {
		run(t, "/srv/race-delete", func(ctx context.Context, tx bun.Tx, f analysisRaceFixture) error {
			_, err := tx.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", f.root.ID).Exec(ctx)
			return err
		}, persistence.ErrSourceAnalysisStale)
	})
}
