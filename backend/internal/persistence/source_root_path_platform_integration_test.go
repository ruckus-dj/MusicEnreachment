//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

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
	if _, err := roots.Create(ctx, "Music", "relative/music", "in_place"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative configured path accepted or rejected without the absolute reason: %v", err)
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
