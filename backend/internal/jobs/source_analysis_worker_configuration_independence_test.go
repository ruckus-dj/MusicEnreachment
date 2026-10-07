package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type configurationWorkerToolsDirectory struct{ path string }

func (directory configurationWorkerToolsDirectory) GetToolsDirectory(context.Context) (string, bool, error) {
	return directory.path, true, nil
}

func TestSourceAnalysisWorkerCombinedConfigFailuresStayStepLocal(t *testing.T) {
	worker := &SourceAnalysisWorker{
		toolsDirectory: configurationWorkerToolsDirectory{path: t.TempDir()},
		platform:       settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}},
	}
	work := &persistence.SourceAnalysisWork{SizeBytes: 12}
	operationID := uuid.New()

	t.Run("invalid fpcalc metadata preserves probe config and fingerprint cache version", func(t *testing.T) {
		snapshot := persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{
			{InstallationID: uuid.New(), Executable: "ffprobe", RelativePath: "ffmpeg/8.0", Version: "8.0"},
			{InstallationID: uuid.New(), Executable: "fpcalc", RelativePath: "chromaprint/1.2.3", Version: "1.2.3", VersionBanner: "invalid banner"},
		}}
		config, err := worker.preparerConfig(context.Background(), snapshot, work, operationID, 1, 2, persistence.SourceStepProbe, persistence.SourceStepFingerprint)
		if err != nil {
			t.Fatalf("combined config returned an error: %v", err)
		}
		if config.ProbeExecutable == "" || config.ProbeFactory == nil || config.FFProbeVersion != "8.0" {
			t.Fatalf("valid probe config was lost: %+v", config)
		}
		if config.FPCalcVersion.Version != "1.2.3" || config.FingerprinterFactory == nil {
			t.Fatalf("fingerprint cache/factory config unavailable: %+v", config)
		}
		if _, err := config.FingerprinterFactory(config.FPCalcExecutable); err == nil {
			t.Fatal("invalid fpcalc metadata unexpectedly allowed a runner")
		}
	})

	t.Run("invalid ffprobe path preserves fingerprint config", func(t *testing.T) {
		snapshot := persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{
			{InstallationID: uuid.New(), Executable: "ffprobe", RelativePath: "../../outside", Version: "8.0"},
			{InstallationID: uuid.New(), Executable: "fpcalc", RelativePath: "chromaprint/1.2.3", Version: "1.2.3", VersionBanner: "fpcalc version 1.2.3"},
		}}
		config, err := worker.preparerConfig(context.Background(), snapshot, work, operationID, 1, 2, persistence.SourceStepProbe, persistence.SourceStepFingerprint)
		if err != nil {
			t.Fatalf("combined config returned an error: %v", err)
		}
		if config.ProbeFactory == nil || config.FPCalcExecutable == "" || config.FingerprinterFactory == nil {
			t.Fatalf("valid fingerprint config or local probe failure missing: %+v", config)
		}
		if _, err := config.ProbeFactory(config.ProbeExecutable); err == nil {
			t.Fatal("invalid ffprobe path unexpectedly allowed a runner")
		}
	})
}
