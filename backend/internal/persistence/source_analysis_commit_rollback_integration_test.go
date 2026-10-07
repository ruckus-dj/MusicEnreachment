//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/driver/pgdriver"
)

// TestSourceAnalysisEnqueueRollsBackAtCommitWithPostgreSQL fails the commit
// itself through a deferred constraint trigger, after the River job and the
// operation row were both written. The transaction must take both rows with it,
// so a commit-time failure never leaves an orphan operation or River job.
func TestSourceAnalysisEnqueueRollsBackAtCommitWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	if _, err := database.ExecContext(ctx, `
		CREATE FUNCTION fail_analysis_commit() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'injected commit failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create the failing commit function: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE CONSTRAINT TRIGGER fail_analysis_commit
		AFTER INSERT ON operation
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW WHEN (NEW.kind = 'analyze_source')
		EXECUTE FUNCTION fail_analysis_commit()`); err != nil {
		t.Fatalf("install the failing commit trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := database.ExecContext(context.Background(),
			`DROP TRIGGER IF EXISTS fail_analysis_commit ON operation`); err != nil {
			t.Errorf("drop the failing commit trigger: %v", err)
		}
		if _, err := database.ExecContext(context.Background(),
			`DROP FUNCTION IF EXISTS fail_analysis_commit()`); err != nil {
			t.Errorf("drop the failing commit function: %v", err)
		}
	})

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-commit-failure")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	operation := normalizedQueuedAnalysis(t, ctx, inventory, root, location, nil)
	requirePostgresError(t, enqueueNormalizedAnalysis(t, ctx, inventory, client, operation), "P0001", "injected commit failure")
	if operations := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 0 {
		t.Fatalf("analysis operations after the failed commit = %d, want 0", operations)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != 0 {
		t.Fatalf("analysis River jobs after the failed commit = %d, want no orphan job", jobs)
	}
}

// requirePostgresError asserts the call failed with the intended PostgreSQL
// error, identified by SQLSTATE and a message fragment. A setup, fence or
// validation failure returns a plain Go error without that SQLSTATE, so it can
// never certify the rollback behavior under test.
func requirePostgresError(t *testing.T, err error, code, messageFragment string) {
	t.Helper()
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want a PostgreSQL error with SQLSTATE %s", err, code)
	}
	if got := pgErr.Field('C'); got != code {
		t.Fatalf("error SQLSTATE = %s (%v), want %s", got, err, code)
	}
	if message := pgErr.Field('M'); !strings.Contains(message, messageFragment) {
		t.Fatalf("error message = %q, want it to contain %q", message, messageFragment)
	}
}

// TestSourceAnalysisEnqueueHoldsPreviousVariantWithPostgreSQL proves that an
// admitted analysis holds its selected work and that the successful probe result
// referenced by that work cannot be deleted while the work is retained.
func TestSourceAnalysisEnqueueHoldsPreviousVariantWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-variant-hold")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	variantID := insertMediaVariantRow(t, ctx, database, location.SizeBytes, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, variantID)
	work := normalizedWork(t, ctx, inventory, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
	)
	previousFailure := "previous probe attempt failed"
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='failed',safe_error=?,success_probe_variant_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='probe'`, previousFailure, variantID, work.ID); err != nil {
		t.Fatalf("retain the previous probe result on the failed retry target: %v", err)
	}
	// The failed probe is an explicit single-step retry target; its successful
	// result remains selected while the retry holds the work.
	step := string(persistence.SourceStepProbe)
	installationID := insertAnalysisInstallation(t, ctx, database, "probe-retry")
	probeTool := normalizedToolSelection(t, ctx, database, installationID, "ffprobe")
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, true, false, []persistence.SourceAnalysisToolSelection{probeTool})
	if err := enqueueNormalizedAnalysis(t, ctx, inventory, client, operation); err != nil {
		t.Fatalf("enqueue a re-analysis: %v", err)
	}
	stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the stored operation: %v", err)
	}
	storedSnapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(stored.InputSnapshot)
	if err != nil {
		t.Fatalf("decode normalized retry snapshot: %v", err)
	}
	if len(storedSnapshot.WorkIDs) != 1 || storedSnapshot.WorkIDs[0] != work.ID || storedSnapshot.TargetWorkID == nil || *storedSnapshot.TargetWorkID != work.ID || storedSnapshot.TargetStep == nil || *storedSnapshot.TargetStep != step || storedSnapshot.RerunTarget == nil || *storedSnapshot.RerunTarget {
		t.Fatalf("normalized retry snapshot = %+v; want explicit non-rerun probe target for held work %s", storedSnapshot, work.ID)
	}
	var heldWorkID uuid.UUID
	if err := database.NewRaw(`SELECT work_id FROM operation_source_work_hold WHERE operation_id=?`, operation.ID).Scan(ctx, &heldWorkID); err != nil {
		t.Fatalf("read persisted selected-work hold: %v", err)
	}
	if heldWorkID != work.ID {
		t.Fatalf("persisted work hold = %s, want %s", heldWorkID, work.ID)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM media_variant WHERE id=?`, variantID); err == nil {
		t.Fatal("deleting the successful probe result referenced by retained work was accepted")
	}
	if !mediaVariantExists(t, ctx, database, variantID) {
		t.Fatal("the held previous variant was removed")
	}
}
