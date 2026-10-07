package jobs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type workerToolsDirectory struct {
	path string
	ok   bool
	err  error
}

func (directory workerToolsDirectory) GetToolsDirectory(context.Context) (string, bool, error) {
	return directory.path, directory.ok, directory.err
}

func TestSourceAnalysisWorkerPreparerConfigPinsOnlyRequestedTool(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	work := &persistence.SourceAnalysisWork{SizeBytes: 17}
	probeID, fpcalcID := uuid.New(), uuid.New()
	snapshot := persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{
		{InstallationID: probeID, Executable: "ffprobe", RelativePath: "ffmpeg/8.0", Version: "8.0"},
		{InstallationID: fpcalcID, Executable: "fpcalc", RelativePath: "chromaprint/1.2.3", Version: "1.2.3", VersionBanner: "fpcalc version 1.2.3"},
	}}
	worker := &SourceAnalysisWorker{toolsDirectory: workerToolsDirectory{path: root, ok: true},
		platform: settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}}

	sha, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepSHA256)
	if err != nil {
		t.Fatalf("SHA config: %v", err)
	}
	if sha.ProbeExecutable != "" || sha.FPCalcExecutable != "" || sha.Hold != nil {
		t.Fatalf("SHA config unexpectedly requires tools: %+v", sha)
	}

	probe, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepProbe)
	if err != nil {
		t.Fatalf("probe config: %v", err)
	}
	if want := filepath.Join(root, "ffmpeg", "8.0", "ffprobe"); probe.ProbeExecutable != want {
		t.Fatalf("probe executable = %q, want %q", probe.ProbeExecutable, want)
	}
	if probe.FPCalcExecutable != "" || probe.FFProbeVersion != "8.0" {
		t.Fatalf("probe config selected unrelated tool or wrong version: %+v", probe)
	}

	fingerprint, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepFingerprint)
	if err != nil {
		t.Fatalf("fingerprint config: %v", err)
	}
	if want := filepath.Join(root, "chromaprint", "1.2.3", "fpcalc"); fingerprint.FPCalcExecutable != want {
		t.Fatalf("fpcalc executable = %q, want %q", fingerprint.FPCalcExecutable, want)
	}
	if fingerprint.ProbeExecutable != "" || fingerprint.FPCalcVersion.Version != "1.2.3" {
		t.Fatalf("fingerprint config selected unrelated tool or wrong version: %+v", fingerprint)
	}
}

func TestSourceAnalysisWorkerPreparerConfigRejectsInvalidPinnedTool(t *testing.T) {
	ctx := context.Background()
	worker := &SourceAnalysisWorker{toolsDirectory: workerToolsDirectory{path: t.TempDir(), ok: true},
		platform: settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}}
	work := &persistence.SourceAnalysisWork{}
	operationID := uuid.New()
	cases := []struct {
		name    string
		tool    persistence.SourceAnalysisToolSelection
		wantErr bool
	}{
		{name: "path escapes managed root", tool: persistence.SourceAnalysisToolSelection{Executable: "ffprobe", RelativePath: "../../outside", Version: "8.0"}, wantErr: true},
		{name: "malformed fpcalc banner", tool: persistence.SourceAnalysisToolSelection{Executable: "fpcalc", RelativePath: "fpcalc/1.2.3", Version: "1.2.3", VersionBanner: "not a version"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := persistence.SourceStepProbe
			if tc.tool.Executable == "fpcalc" {
				step = persistence.SourceStepFingerprint
			}
			_, err := worker.preparerConfig(ctx, persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{tc.tool}}, work, operationID, 1, 2, step)
			if (err != nil) != tc.wantErr {
				t.Fatalf("preparerConfig error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	worker.toolsDirectory = workerToolsDirectory{}
	if _, err := worker.preparerConfig(ctx, persistence.SourceAnalysisOperationSnapshot{}, work, operationID, 1, 2, persistence.SourceStepProbe); err == nil {
		t.Fatal("missing managed tools directory accepted")
	}
}
