//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const fallbackRetirementMigration = "20261207000000"

func TestAutomaticFallbackRetirementRollbackRefusesLiveAuthorizationPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, fallbackRetirementMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackRetirementMigration))
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	applyFallbackRetirementRiverSchema(t, ctx, database)

	runID, _ := insertFallbackReceiptMigrationScope(t, ctx, database)
	finishedAt := time.Now().UTC().Add(-96 * time.Hour)
	if _, err := database.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, finishedAt, finishedAt, runID).Exec(ctx); err != nil {
		t.Fatalf("age matching operation past the retirement cutoff: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run_retirement_authorization(run_id) VALUES (?)`, runID).Exec(ctx); err != nil {
		t.Fatalf("create eligible live retirement authorization: %v", err)
	}

	rollbackMigrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := rollbackMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize rollback migrator: %v", err)
	}
	if _, err := rollbackMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back fallback retirement while authorizations remain") {
		t.Fatalf("rollback error=%v, want populated-authorization safety guard", err)
	}

	var authCount int
	if err := database.NewRaw(`SELECT count(*) FROM matching_run_retirement_authorization WHERE run_id=?`, runID).Scan(ctx, &authCount); err != nil {
		t.Fatalf("read retained authorization: %v", err)
	}
	if authCount != 1 {
		t.Fatalf("blocked rollback retained %d authorizations, want 1", authCount)
	}
	var tableExists, cutoffColumn bool
	if err := database.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='matching_run_retirement_authorization'
	)`).Scan(ctx, &tableExists); err != nil {
		t.Fatalf("check retained authorization table: %v", err)
	}
	if err := database.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='matching_run_retirement_authorization' AND column_name='cleanup_cutoff'
	)`).Scan(ctx, &cutoffColumn); err != nil {
		t.Fatalf("check retained authorization schema: %v", err)
	}
	if !tableExists || !cutoffColumn {
		t.Fatalf("blocked rollback changed authorization schema: table exists=%v cleanup_cutoff exists=%v", tableExists, cutoffColumn)
	}
}

func applyFallbackRetirementRiverSchema(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	// The retirement guard checks for live River jobs even though this fixture
	// intentionally creates none. Install River's schema through its official
	// migrator so those eligibility queries execute against the real table.
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
}
