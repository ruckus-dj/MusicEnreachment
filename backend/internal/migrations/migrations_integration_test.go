//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestMigrationsApplyAndRollbackWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	var tableCount int
	if err := database.NewRaw(`
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_name IN ('app_setting', 'tool_installation', 'operation')
	`).Scan(ctx, &tableCount); err != nil {
		t.Fatalf("count setup-manager tables: %v", err)
	}
	if tableCount != 3 {
		t.Fatalf("setup-manager table count = %d, want 3", tableCount)
	}
	var validatedConstraintCount int
	if err := database.NewRaw(`
		SELECT count(*)
		FROM pg_constraint
		WHERE conname IN (
			'tool_installation_ready_is_verified',
			'operation_mutation_has_target_installation'
		)
		  AND convalidated
	`).Scan(ctx, &validatedConstraintCount); err != nil {
		t.Fatalf("count validated setup-manager constraints: %v", err)
	}
	if validatedConstraintCount != 2 {
		t.Fatalf("validated setup-manager constraints = %d, want 2", validatedConstraintCount)
	}
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback setup-manager migration: %v", err)
	}
	if group == nil {
		t.Fatal("rollback setup-manager migration returned no migration group")
	}
	var operationTable *string
	if err := database.NewRaw(`SELECT to_regclass('public.operation')`).Scan(ctx, &operationTable); err != nil {
		t.Fatalf("check rolled back operation table: %v", err)
	}
	if operationTable != nil {
		t.Fatalf("operation table remains after rollback: %s", *operationTable)
	}
}
