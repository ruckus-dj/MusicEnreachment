package jobs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestSourceAnalysisProbeFactoryVerifiesFreshFFmpegPairAgainstCanonicalVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a Unix executable script")
	}

	const ffprobeBanner = "ffprobe version v8.0-custom\nCopyright (c) fixture\nBuild details retained verbatim\n"
	const canonical = "ffmpeg n8.0-custom + ffprobe v8.0-custom"

	for _, test := range []struct {
		name          string
		ffmpegVersion string
		wantError     bool
	}{
		{name: "fresh pair matches opaque canonical version", ffmpegVersion: "n8.0-custom"},
		{name: "changed ffmpeg sibling rejects unchanged ffprobe", ffmpegVersion: "n8.1-custom", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			packageDir := filepath.Join(root, "ffmpeg-package")
			if err := os.MkdirAll(packageDir, 0o755); err != nil {
				t.Fatal(err)
			}
			ffmpegPath := filepath.Join(packageDir, "ffmpeg")
			script := "#!/bin/sh\nprintf '%s\\n' 'ffmpeg version " + test.ffmpegVersion + "' 'Copyright (c) fixture'\n"
			if err := os.WriteFile(ffmpegPath, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			worker := &SourceAnalysisWorker{
				toolsDirectory: workerToolsDirectory{path: root, ok: true},
				platform:       settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}},
				verifyFFProbeVersion: func(context.Context, string) (string, error) {
					return ffprobeBanner, nil
				},
			}
			config, err := worker.preparerConfig(context.Background(), persistence.SourceAnalysisOperationSnapshot{
				Tools: []persistence.SourceAnalysisToolSelection{{
					InstallationID: uuid.New(), Executable: "ffprobe", RelativePath: "ffmpeg-package",
					Version: canonical, VersionBanner: ffprobeBanner,
				}},
			}, &persistence.SourceAnalysisWork{SizeBytes: 1}, uuid.New(), 1, 2, persistence.SourceStepProbe)
			if err != nil {
				t.Fatalf("build probe config: %v", err)
			}
			if config.FFProbeVersion != ffprobeBanner {
				t.Fatalf("cache version bytes = %q, want exact pinned ffprobe banner", config.FFProbeVersion)
			}
			_, factoryErr := config.ProbeFactory(config.ProbeExecutable)
			if test.wantError {
				if factoryErr == nil {
					t.Fatal("factory accepted changed ffmpeg sibling with unchanged ffprobe")
				}
				return
			}
			if factoryErr != nil {
				t.Fatalf("factory rejected matching fresh pair: %v", factoryErr)
			}
		})
	}
}
