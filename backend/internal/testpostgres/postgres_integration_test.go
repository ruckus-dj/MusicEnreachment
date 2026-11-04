//go:build integration

package testpostgres

import (
	"context"
	"testing"

	"github.com/uptrace/bun"
)

func TestOpenCreatesIndependentEmptyAndMigratedDatabases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	empty := Open(t)
	var emptyHasOperation bool
	if err := empty.NewRaw(`SELECT to_regclass('public.operation') IS NOT NULL`).Scan(ctx, &emptyHasOperation); err != nil {
		t.Fatalf("inspect empty database schema: %v", err)
	}
	if emptyHasOperation {
		t.Fatal("Open returned a database with the application schema")
	}

	first := OpenMigrated(t)
	second := OpenMigrated(t)
	for label, database := range map[string]*bun.DB{"first": first, "second": second} {
		var hasOperation bool
		if err := database.NewRaw(`SELECT to_regclass('public.operation') IS NOT NULL`).Scan(ctx, &hasOperation); err != nil {
			t.Fatalf("inspect %s migrated schema: %v", label, err)
		}
		if !hasOperation {
			t.Fatalf("%s database is missing the migrated application schema", label)
		}
	}
	if _, err := first.ExecContext(ctx, `CREATE TABLE testpostgres_isolation_probe (value integer)`); err != nil {
		t.Fatalf("mutate first migrated database: %v", err)
	}
	var leaked bool
	if err := second.NewRaw(`SELECT to_regclass('public.testpostgres_isolation_probe') IS NOT NULL`).Scan(ctx, &leaked); err != nil {
		t.Fatalf("inspect second migrated database: %v", err)
	}
	if leaked {
		t.Fatal("a mutation in one migrated database leaked into another")
	}
	var appHasRiver bool
	if err := first.NewRaw(`SELECT to_regclass('public.river_job') IS NOT NULL`).Scan(ctx, &appHasRiver); err != nil {
		t.Fatalf("inspect application-only template for River schema: %v", err)
	}
	if appHasRiver {
		t.Fatal("OpenMigrated unexpectedly includes the River schema")
	}
	riverDB := OpenMigratedWithRiver(t)
	var hasRiver, hasJobs bool
	if err := riverDB.NewRaw(`SELECT to_regclass('public.river_job') IS NOT NULL`).Scan(ctx, &hasRiver); err != nil {
		t.Fatalf("inspect River template schema: %v", err)
	}
	if !hasRiver {
		t.Fatal("OpenMigratedWithRiver is missing River schema")
	}
	if err := riverDB.NewRaw(`SELECT EXISTS (SELECT 1 FROM river_job)`).Scan(ctx, &hasJobs); err != nil {
		t.Fatalf("inspect River template jobs: %v", err)
	}
	if hasJobs {
		t.Fatal("River template contains test jobs")
	}
	t.Run("concurrent River clones are isolated", func(t *testing.T) {
		for _, name := range []string{"first", "second"} {
			name := name
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				db := OpenMigratedWithRiver(t)
				if _, err := db.ExecContext(ctx, `CREATE TABLE river_clone_probe (value integer)`); err != nil {
					t.Fatalf("mutate %s clone: %v", name, err)
				}
				var hasOther bool
				if err := db.NewRaw(`SELECT to_regclass('public.river_clone_probe') IS NOT NULL`).Scan(ctx, &hasOther); err != nil || !hasOther {
					t.Fatalf("inspect %s own clone: exists=%v err=%v", name, hasOther, err)
				}
			})
		}
	})
}

func TestDropRegistryRetainsFailedDropsAndRemovesSuccessfulDrops(t *testing.T) {
	const name = "testpostgres_registry_probe"
	registerResource(name)
	t.Cleanup(func() { unregisterResource(name) })
	if err := dropAndUnregister(name, func(string) error { return context.DeadlineExceeded }); err == nil {
		t.Fatal("failed drop returned no error")
	}
	if !hasResource(name) {
		t.Fatal("failed drop removed resource from process cleanup registry")
	}
	if err := dropAndUnregister(name, func(string) error { return nil }); err != nil {
		t.Fatalf("successful drop: %v", err)
	}
	if hasResource(name) {
		t.Fatal("successful drop remained in process cleanup registry")
	}
}

func hasResource(name string) bool {
	process.resourcesMu.Lock()
	defer process.resourcesMu.Unlock()
	for _, resource := range process.resources {
		if resource == name {
			return true
		}
	}
	return false
}
