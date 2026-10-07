//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

// sourceRootPathPlatformMigration is the version (the file-name timestamp) of
// the relaxation migration whose down pair the rollback test isolates.
const sourceRootPathPlatformMigration = "20261004000000"

// TestSourceRootConfiguredPathPlatformsWithPostgreSQL pins the two path
// boundaries the platform fix separates: the database stores any platform
// absolute path after the relaxation, while the service still refuses a
// relative path before it reaches the repository. It proves the strings, not the
// runtime filesystem: "C:\\Music" is a valid Windows path that cannot exist on
// this Unix runner.
func TestSourceRootConfiguredPathPlatformsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	for _, configuredPath := range []string{"/srv/music", `C:\Music`} {
		root := &persistence.SourceRoot{ConfiguredPath: configuredPath, DisplayName: configuredPath, Enabled: true}
		if err := inventory.CreateSourceRoot(ctx, root); err != nil {
			t.Fatalf("store the configured path %q: %v", configuredPath, err)
		}
		stored, err := inventory.GetSourceRoot(ctx, root.ID)
		if err != nil {
			t.Fatalf("read back the configured path %q: %v", configuredPath, err)
		}
		if stored.ConfiguredPath != configuredPath {
			t.Fatalf("stored configured path = %q, want %q", stored.ConfiguredPath, configuredPath)
		}
	}

	roots := service.NewSourceRoots(inventory, emptyManagedPaths{})
	if _, err := roots.Create(ctx, "Music", "relative/music"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative configured path accepted or rejected without the absolute reason: %v", err)
	}
}

// TestSourceRootConfiguredPathConstraintRollbackWithPostgreSQL applies every
// migration except the relaxation, then the relaxation as its own migration
// group, so Rollback removes exactly that pair and leaves the schema otherwise
// intact. It proves the down migration restores the POSIX-only check.
func TestSourceRootConfiguredPathConstraintRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	full, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	before := migrate.NewMigrations()
	for _, migration := range full.Sorted() {
		if migration.Name == sourceRootPathPlatformMigration {
			continue
		}
		before.Add(migration)
	}

	beforeMigrator := migrate.NewMigrator(database, before, migrate.WithMarkAppliedOnSuccess(true))
	if err := beforeMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize migrations before the relaxation: %v", err)
	}
	if _, err := beforeMigrator.Migrate(ctx); err != nil {
		t.Fatalf("apply migrations before the relaxation: %v", err)
	}

	migrator := migrate.NewMigrator(database, full, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize the full migration set: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply the path relaxation: %v", err)
	}

	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("roll back the path relaxation: %v", err)
	}
	if group == nil || len(group.Migrations) != 1 || group.Migrations[0].Name != sourceRootPathPlatformMigration {
		t.Fatalf("rolled back group = %+v, want only migration %s", group, sourceRootPathPlatformMigration)
	}

	inventory := persistence.NewSourceInventoryRepository(database)
	if err := inventory.CreateSourceRoot(ctx, &persistence.SourceRoot{
		ConfiguredPath: "/srv/music", DisplayName: "POSIX", Enabled: true,
	}); err != nil {
		t.Fatalf("POSIX configured path rejected after the rollback: %v", err)
	}
	windows := &persistence.SourceRoot{ConfiguredPath: `C:\Music`, DisplayName: "Windows", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, windows); err == nil {
		t.Fatal("the restored POSIX-only check accepted a Windows configured path")
	} else if !strings.Contains(err.Error(), "source_root_configured_path_absolute") {
		t.Fatalf("Windows configured path rejected by an unexpected constraint: %v", err)
	}
}

// emptyManagedPaths is the empty managed-paths reader the service needs to reach
// its own relative-path rejection without an installed tools or output root.
type emptyManagedPaths struct{}

func (emptyManagedPaths) GetToolsDirectory(context.Context) (string, bool, error) {
	return "", false, nil
}
func (emptyManagedPaths) GetOutputDirectory(context.Context) (string, bool, error) {
	return "", false, nil
}
