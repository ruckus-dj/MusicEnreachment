//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// These PostgreSQL fixtures represent database state only. They do not claim
// that any executable was downloaded or verified.

func c07SeedLegacyReadyInstallation(t *testing.T, ctx context.Context, repo *persistence.SetupManagerRepository, version string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	relative, err := tools.ManagedRelativePath(tools.PackageFFmpeg, version)
	if err != nil {
		t.Fatal(err)
	}
	relative = filepath.ToSlash(relative)
	installation := &persistence.ToolInstallation{
		ID: id, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "legacy-test", ReleaseIdentity: version, RelativePath: relative,
		State: "preparing", ExecutableVerificationOutput: json.RawMessage(`{"ffmpeg":"ffmpeg version ` + version + `","ffprobe":"ffprobe version ` + version + `"}`), ArtifactIdentities: json.RawMessage(`[]`),
	}
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("seed synthetic legacy installation: %v", err)
	}
	if err := repo.MarkInstallationReady(ctx, id, json.RawMessage(`{"ffmpeg":"ffmpeg version `+version+`","ffprobe":"ffprobe version `+version+`"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark synthetic legacy installation ready: %v", err)
	}
	return id
}
