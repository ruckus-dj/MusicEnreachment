package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPublicationPinnedToolsResolveWindowsNativeEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ffmpeg", "release-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	const ffmpegBanner = "ffmpeg version 7.1.1 build"
	const ffprobeBanner = "ffprobe version 6.1.1 build"
	version := "ffmpeg 7.1.1 + ffprobe 6.1.1"
	id := uuid.New()
	versions, err := json.Marshal(map[string]string{"ffmpeg.exe": ffmpegBanner, "ffprobe.exe": ffprobeBanner})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	repo := &pinnedToolRepo{
		root:  root,
		valid: true,
		installation: &persistence.ToolInstallation{
			ID: id, PackageKind: "ffmpeg", PlatformGOOS: "windows", PlatformGOARCH: "amd64",
			RelativePath: "ffmpeg/release-1", Version: version, State: "ready", VerifiedAt: &now,
			ExecutableVerificationOutput: versions,
		},
		selections: []persistence.SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: id, RelativePath: "ffmpeg/release-1", Executable: "ffmpeg", Version: version, VersionBanner: ffmpegBanner},
			{PackageKind: "ffmpeg", InstallationID: id, RelativePath: "ffmpeg/release-1", Executable: "ffprobe", Version: version, VersionBanner: ffprobeBanner},
		},
	}
	runner := pinnedToolRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch filepath.Base(name) {
		case "ffmpeg.exe":
			return []byte(ffmpegBanner + "\n"), nil
		case "ffprobe.exe":
			return []byte(ffprobeBanner + "\n"), nil
		default:
			t.Fatalf("unexpected executable path %q", name)
			return nil, nil
		}
	})

	pair, ok, err := NewPublicationPinnedToolsResolver(repo, runner, tools.Platform{GOOS: "windows", GOARCH: "amd64"}).Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, repo.selections)
	if err != nil || !ok {
		t.Fatalf("Resolve() = (%+v, %v, %v)", pair, ok, err)
	}
	if filepath.Base(pair.FFmpeg) != "ffmpeg.exe" || filepath.Base(pair.FFprobe) != "ffprobe.exe" || pair.FFmpeg == pair.FFprobe {
		t.Fatalf("Resolve() returned paths %q and %q", pair.FFmpeg, pair.FFprobe)
	}
}
