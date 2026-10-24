//go:build integration

package migrations_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

// The unpublished prototype keeps the five fresh-instance baselines as its
// historical foundation. Later additive migrations apply on top and must not
// be folded into the baseline identity.
var baselineMigrationNames = []string{
	"20261015000001",
	"20261015000002",
	"20261015000003",
	"20261015000004",
	"20261015000005",
}

var additiveMigrationNames = []string{
	"20261017000001",
	"20261017000002",
	"20261017000003",
	"20261017000004",
	"20261017000005",
	"20261017000006",
	"20261017000007",
	"20261018000001",
	"20261020000001",
	"20261021000001",
	"20261022000001",
	"20261023000001",
	"20261024000001",
	"20261025000001",
	"20261025000002",
	"20261026000001",
	"20261027000001",
	"20261028000001",
	"20261029000001",
	"20261029000002",
	"20261029000003",
	"20261030000001",
	"20261030000002",
	"20261030000003",
	"20261030000004",
	"20261030000005",
}

var currentMigrationNames = append(append([]string(nil), baselineMigrationNames...), additiveMigrationNames...)

func TestCurrentMigrationsApplyIdempotentlyAndRollbackAsOneGroup(t *testing.T) {
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
	assertMigrationInventory(t, collection, currentMigrationNames)

	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize current migrations: %v", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply current migrations: %v", err)
	}
	assertMigrationGroup(t, group, currentMigrationNames, "applied")
	assertCurrentSchema(t, db)
	if group, err = migrator.Migrate(ctx); err != nil || group == nil || len(group.Migrations) != 0 {
		t.Fatalf("reapplying current schema = %#v, %v; want an empty migration group", group, err)
	}

	rolledBack, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback clean migration group: %v", err)
	}
	assertMigrationGroup(t, rolledBack, currentMigrationNames, "rolled back")
	for _, table := range []string{"app_setting", "operation", "source_root", "media_metadata_result", "release_track", "publication_manual_lyrics_receipt"} {
		if relationName(t, db, table) != nil {
			t.Errorf("application table %s remains after current rollback", table)
		}
	}
	var riverState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=1`).Scan(ctx, &riverState); err != nil || riverState != "available" {
		t.Fatalf("River data after clean rollback = %q, %v; want unchanged", riverState, err)
	}
	group, err = migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("reapply current migration group: %v", err)
	}
	assertMigrationGroup(t, group, currentMigrationNames, "reapplied")
	assertCurrentSchema(t, db)
}

func TestUnsafeBaselineRollbackIsAtomic(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	// This test targets the baseline rollback guard. Additive down migrations
	// run as separate transactions before the guarded baseline down file, so a
	// full current collection would change the schema before the refusal. The
	// current collection's clean rollback is covered by the test above.
	collection := loadBaselineMigrations(t)
	assertMigrationInventory(t, collection, baselineMigrationNames)
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
		t.Fatalf("load application migrations: %v", err)
	}
	return collection
}

func assertMigrationInventory(t *testing.T, collection *migrate.Migrations, want []string) {
	t.Helper()
	got := collection.Sorted()
	if len(got) != len(want) {
		t.Fatalf("discovered %d migrations, want %d: %#v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("discovered migration %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func loadBaselineMigrations(t *testing.T) *migrate.Migrations {
	t.Helper()
	baseline := migrate.NewMigrations()
	for _, migration := range loadMigrations(t).Sorted() {
		if slices.Contains(baselineMigrationNames, migration.Name) {
			baseline.Add(migration)
		}
	}
	assertMigrationInventory(t, baseline, baselineMigrationNames)
	return baseline
}

func assertMigrationGroup(t *testing.T, group *migrate.MigrationGroup, want []string, direction string) {
	t.Helper()
	if group == nil {
		t.Fatalf("%s migration group is nil", direction)
	}
	got := make([]string, 0, len(group.Migrations))
	for _, migration := range group.Migrations {
		got = append(got, migration.Name)
	}
	// Bun returns rollback groups in reverse execution order.
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("%s migration names = %v, want %v", direction, got, want)
	}
}

func assertCurrentSchema(t *testing.T, database *bun.DB) {
	t.Helper()
	var nativeColumns, nativeConstraints, identityColumns, identityConstraints, positionMetadataColumns, coverObserved, coverConstraint, sourceMetadataReference int
	ctx := context.Background()
	checks := []struct {
		query  string
		result *int
		want   int
	}{
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='media_metadata_result' AND column_name='native_matroska'`, &nativeColumns, 0},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.media_metadata_result'::regclass AND conname='media_metadata_result_native_matroska_check'`, &nativeConstraints, 0},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='release_track' AND column_name='id' AND data_type='uuid' AND is_nullable='NO'`, &identityColumns, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.release_track'::regclass AND conname='release_track_id_key' AND contype='u'`, &identityConstraints, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='release_track' AND column_name='metadata' AND data_type='jsonb' AND is_nullable='NO'`, &positionMetadataColumns, 1},
		{`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='media_metadata_result' AND column_name='cover_observed' AND is_nullable='NO'`, &coverObserved, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.media_metadata_result'::regclass AND conname='media_metadata_result_cover_facts_check'`, &coverConstraint, 1},
		{`SELECT count(*) FROM pg_constraint WHERE conrelid='public.track_source'::regclass AND conname='track_source_source_metadata_result_fk' AND confdeltype='r'`, &sourceMetadataReference, 1},
	}
	for _, check := range checks {
		if err := database.NewRaw(check.query).Scan(ctx, check.result); err != nil {
			t.Fatalf("inspect current schema: %v", err)
		}
		if *check.result != check.want {
			t.Fatalf("schema check %q = %d, want %d", check.query, *check.result, check.want)
		}
	}
}
