//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func canonicalAnalysisRootPath(t *testing.T, name string) string {
	t.Helper()
	path, err := os.MkdirTemp(t.TempDir(), name+"-")
	if err != nil {
		t.Fatalf("create source analysis root path: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize source analysis root path: %v", err)
	}
	return canonical
}

// establishInventory makes a freshly created root the non-stale root of an
// applied generation, which is what an analysis snapshot requires.
func establishInventory(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = ?", root.ConfiguredPath).
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the root inventory: %v", err)
	}
	root.ScanGeneration = 1
	inventoryPath := root.ConfiguredPath
	root.InventoryPath = &inventoryPath
}

func insertAnalysisLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string, sizeBytes int64, mtime time.Time) persistence.SourceLocation {
	t.Helper()
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: sizeBytes,
		Mtime: mtime.Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert analysis location %s: %v", relativePath, err)
	}
	return location
}

func insertAnalysisInstallation(t *testing.T, ctx context.Context, database *bun.DB, identity string) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	relativePath := testManagedRelativePath(t, tools.PackageFFmpeg, "darwin", identity)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: identity, RelativePath: relativePath,
		State: "ready", ExecutableVerificationOutput: verifiedExecutableVersionMetadata(t, tools.PackageFFmpeg, "darwin", "7.1.2"),
		Version:            canonicalVerifiedFixtureVersion(t, tools.PackageFFmpeg, "7.1.2"),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert analysis installation %s: %v", identity, err)
	}
	return installation.ID
}

// Persisted installation paths follow the recorded platform, not the test host.
func testManagedRelativePath(t *testing.T, kind tools.PackageKind, goos, identity string) string {
	t.Helper()
	relativePath, err := tools.ManagedRelativePath(kind, identity)
	if err != nil {
		t.Fatalf("managed test installation path: %v", err)
	}
	if goos == "windows" {
		return strings.ReplaceAll(relativePath, "/", `\`)
	}
	return filepath.ToSlash(relativePath)
}

func verifiedExecutableVersionMetadata(t *testing.T, kind tools.PackageKind, goos, version string) json.RawMessage {
	t.Helper()
	versions := make(map[string]string)
	for _, executable := range tools.ExpectedExecutables(kind, goos) {
		name := executable
		if goos == "windows" {
			name = strings.TrimSuffix(name, ".exe")
		}
		versions[executable] = name + " version " + version
	}
	encoded, err := json.Marshal(versions)
	if err != nil {
		t.Fatalf("encode verified executable metadata: %v", err)
	}
	return encoded
}

// canonicalVerifiedFixtureVersion pairs explicit fixture evidence with the
// canonical version expected for that same fixture. It does not synthesize
// production metadata or fill missing installation values.
func canonicalVerifiedFixtureVersion(t *testing.T, kind tools.PackageKind, version string) string {
	t.Helper()
	switch kind {
	case tools.PackageFFmpeg:
		return "ffmpeg " + version + " + ffprobe " + version
	case tools.PackageFPCalc:
		return version
	default:
		t.Fatalf("unsupported verified tool fixture kind %q", kind)
		return ""
	}
}

func insertMediaVariantRow(t *testing.T, ctx context.Context, database *bun.DB, sizeBytes int64, appliedOperationID uuid.UUID) uuid.UUID {
	t.Helper()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: sizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(), AppliedOperationID: appliedOperationID,
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("insert media variant: %v", err)
	}
	return variant.ID
}

func linkLocationVariant(t *testing.T, ctx context.Context, database *bun.DB, locationID, variantID uuid.UUID) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variantID).Set("updated_at = now()").
		Where("id = ?", locationID).Exec(ctx); err != nil {
		t.Fatalf("link location %s to variant %s: %v", locationID, variantID, err)
	}
}

func mediaVariantExists(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM media_variant WHERE id = ?", id).Scan(ctx, &count); err != nil {
		t.Fatalf("read media variant %s: %v", id, err)
	}
	return count == 1
}

func analysisJobs(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM river_job").Scan(ctx, &count); err != nil {
		t.Fatalf("count River jobs: %v", err)
	}
	return count
}
