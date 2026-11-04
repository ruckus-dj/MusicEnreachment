//go:build integration

package testpostgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	postgres "github.com/fergusstrange/embedded-postgres/v2"
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

func TestEmbeddedStopReclaimsOwnedClusterWorkDirectory(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") != "" {
		t.Skip("embedded cluster lifecycle is not used with an external PostgreSQL server")
	}
	parent := t.TempDir()
	sentinel := filepath.Join(parent, "parent-sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatalf("write parent sentinel: %v", err)
	}
	server := postgres.NewDatabase(postgres.DefaultConfig().Version(postgres.V18).Port(0).Storage(postgres.Storage{WorkDir: parent}))
	if err := server.Start(); err != nil {
		t.Fatalf("start independent embedded PostgreSQL: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			if err := server.Stop(); err != nil {
				t.Errorf("stop independent embedded PostgreSQL after test failure: %v", err)
			}
		}
	})
	info := server.Info()
	if info.WorkDir == "" || filepath.Dir(info.WorkDir) != parent {
		t.Fatalf("embedded work directory %q is not a child of %q", info.WorkDir, parent)
	}
	if info.DataDir == "" || filepath.Dir(info.DataDir) != info.WorkDir {
		t.Fatalf("default data directory %q is not owned by work directory %q", info.DataDir, info.WorkDir)
	}
	admin, err := sql.Open("pgx", server.ConnectionURL())
	if err != nil {
		_ = server.Stop()
		t.Fatalf("open independent embedded admin pool: %v", err)
	}
	for _, name := range []string{"testpostgres_cleanup_one", "testpostgres_cleanup_two"} {
		if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+quoteIdentifier(name)); err != nil {
			_ = admin.Close()
			_ = server.Stop()
			t.Fatalf("create independent cluster database %s: %v", name, err)
		}
	}
	if err := admin.Close(); err != nil {
		t.Fatalf("close independent embedded admin pool: %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("stop independent embedded PostgreSQL: %v", err)
	}
	stopped = true
	if _, err := os.Stat(info.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("owned work directory remains after stop: stat err=%v", err)
	}
	if contents, err := os.ReadFile(sentinel); err != nil || string(contents) != "preserve" {
		t.Fatalf("parent sentinel after stop = %q, err=%v", contents, err)
	}
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
