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
	installationID := insertAnalysisInstallation(t, ctx, database, "commit-failure")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)

	operation := analysisEnqueueOperation(root, location, installationID, nil)
	requirePostgresError(t, enqueueAnalysis(t, ctx, inventory, operation, client), "P0001", "injected commit failure")
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

// TestSourceAnalysisEnqueueHoldsPreviousVariantWithPostgreSQL proves the second
// read hold: an analysis of an already analyzed file pins the current variant in
// analysis_media_variant_id and in the immutable snapshot, so the variant stays
// alive until the operation reaches its terminal state.
func TestSourceAnalysisEnqueueHoldsPreviousVariantWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-variant-hold")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "variant-hold")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)
	variantID := insertMediaVariantRow(t, ctx, database, location.SizeBytes, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, variantID)
	location.MediaVariantID = &variantID

	operation := analysisEnqueueOperation(root, location, installationID, &variantID)
	if err := enqueueAnalysis(t, ctx, inventory, operation, client); err != nil {
		t.Fatalf("enqueue a re-analysis: %v", err)
	}
	stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the stored operation: %v", err)
	}
	if stored.AnalysisMediaVariantID == nil || *stored.AnalysisMediaVariantID != variantID {
		t.Fatalf("variant hold = %v, want the current %s", stored.AnalysisMediaVariantID, variantID)
	}
	snapshot := mustDecodeSnapshot(t, stored.InputSnapshot)
	if snapshot.PreviousVariantID == nil || *snapshot.PreviousVariantID != variantID {
		t.Fatalf("snapshot previous variant = %v, want %s", snapshot.PreviousVariantID, variantID)
	}
	if !mediaVariantExists(t, ctx, database, variantID) {
		t.Fatal("the held previous variant was removed")
	}
}
