//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func readyFPCalc(t *testing.T, releaseIdentity, version string) *persistence.ToolInstallation {
	t.Helper()
	relativePath := testManagedRelativePath(t, tools.PackageFPCalc, "darwin", releaseIdentity)
	verifiedAt := time.Now().UTC()
	return &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: releaseIdentity, RelativePath: relativePath,
		State: "ready", ExecutableVerificationOutput: verifiedExecutableVersionMetadata(t, tools.PackageFPCalc, "darwin", version),
		Version:            canonicalVerifiedFixtureVersion(t, tools.PackageFPCalc, version),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
}

func detailWork(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, location persistence.SourceLocation) *persistence.SourceAnalysisWork {
	t.Helper()
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		OriginScanOperationID: uuid.New(),
	}
	if _, err := database.NewInsert().Model(work).Exec(ctx); err != nil {
		t.Fatalf("insert source analysis work: %v", err)
	}
	return work
}
