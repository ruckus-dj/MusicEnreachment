//go:build integration

package migrations_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

var baselineMigrationNames = []string{
	"20261015000001",
	"20261015000002",
	"20261015000003",
	"20261015000004",
	"20261015000005",
}

func TestBaselineMigrationsApplyIdempotentlyAndRollbackAsOneGroup(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE river_job (id bigint PRIMARY KEY, state text NOT NULL)`); err != nil {
		t.Fatalf("create River-owned table fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO river_job(id,state) VALUES(1,'available')`); err != nil {
		t.Fatalf("insert River-owned row: %v", err)
	}
	collection := loadMigrations(t)
	assertBaselineMigrationInventory(t, collection)

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize baseline migrations: %v", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	if group == nil || len(group.Migrations) != len(baselineMigrationNames) {
		t.Fatalf("applied migration group = %#v, want the five baseline migrations", group)
	}
	for i, name := range baselineMigrationNames {
		if group.Migrations[i].Name != name {
			t.Fatalf("migration %d = %q, want %q", i, group.Migrations[i].Name, name)
		}
	}
	if group, err = migrator.Migrate(ctx); err != nil || group == nil || len(group.Migrations) != 0 {
		t.Fatalf("reapplying current schema = %#v, %v; want an empty migration group", group, err)
	}

	rolledBack, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback clean baseline group: %v", err)
	}
	if rolledBack == nil || len(rolledBack.Migrations) != len(baselineMigrationNames) {
		t.Fatalf("rollback group = %#v, want all five baseline migrations", rolledBack)
	}
	for _, table := range []string{"app_setting", "operation", "source_root"} {
		if relationName(t, db, table) != nil {
			t.Errorf("application table %s remains after baseline rollback", table)
		}
	}
	var riverState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=1`).Scan(ctx, &riverState); err != nil || riverState != "available" {
		t.Fatalf("River data after clean rollback = %q, %v; want unchanged", riverState, err)
	}
	group, err = migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("reapply baseline group: %v", err)
	}
	if group == nil || len(group.Migrations) != len(baselineMigrationNames) {
		t.Fatalf("reapplied migration group = %#v, want all five baseline migrations", group)
	}
}

func TestUnsafeBaselineRollbackIsAtomic(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection := loadMigrations(t)
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize baseline migrations: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE river_job (id bigint PRIMARY KEY, state text NOT NULL)`); err != nil {
		t.Fatalf("create River-owned table fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO river_job(id,state) VALUES(1,'available')`); err != nil {
		t.Fatalf("insert River-owned row: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES('rollback-guard','preserve')`); err != nil {
		t.Fatalf("insert rollback-guard data: %v", err)
	}
	statusBefore, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		t.Fatalf("read migration status before unsafe rollback: %v", err)
	}
	var schemaTablesBefore int
	if err := db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(ctx, &schemaTablesBefore); err != nil {
		t.Fatalf("count application tables before unsafe rollback: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback with live application data succeeded")
	}
	var setting string
	if err := db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='rollback-guard'`).Scan(ctx, &setting); err != nil || setting != "preserve" {
		t.Fatalf("rollback-guard data after refusal = %q, %v; want unchanged", setting, err)
	}
	var riverState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=1`).Scan(ctx, &riverState); err != nil || riverState != "available" {
		t.Fatalf("River data after refusal = %q, %v; want unchanged", riverState, err)
	}
	var riverTable string
	if err := db.NewRaw(`SELECT to_regclass('public.river_job')::text`).Scan(ctx, &riverTable); err != nil || riverTable == "" {
		t.Fatalf("River table after refusal = %q, %v; want it intact", riverTable, err)
	}
	statusAfter, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		t.Fatalf("read migration status after unsafe rollback: %v", err)
	}
	if fmt.Sprint(statusAfter) != fmt.Sprint(statusBefore) {
		t.Fatalf("migration status changed after refused rollback: before=%#v after=%#v", statusBefore, statusAfter)
	}
	var schemaTablesAfter int
	if err := db.NewRaw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(ctx, &schemaTablesAfter); err != nil {
		t.Fatalf("count application tables after unsafe rollback: %v", err)
	}
	if schemaTablesAfter != schemaTablesBefore {
		t.Fatalf("schema table count after refused rollback = %d, want unchanged count %d", schemaTablesAfter, schemaTablesBefore)
	}
}

func loadMigrations(t *testing.T) *migrate.Migrations {
	t.Helper()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load baseline migrations: %v", err)
	}
	return collection
}

func assertBaselineMigrationInventory(t *testing.T, collection *migrate.Migrations) {
	t.Helper()
	got := collection.Sorted()
	if len(got) != len(baselineMigrationNames) {
		t.Fatalf("discovered %d migrations, want exactly the five current baselines: %#v", len(got), got)
	}
	for i, name := range baselineMigrationNames {
		if got[i].Name != name {
			t.Fatalf("discovered migration %d = %q, want %q", i, got[i].Name, name)
		}
	}
}
