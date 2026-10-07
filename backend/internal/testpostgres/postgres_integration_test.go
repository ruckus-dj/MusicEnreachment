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
}
