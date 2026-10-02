//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

// analysisDispatchRelease is the release identity of the fake managed ffmpeg the
// analysis dispatch tests install.
const analysisDispatchRelease = "1.6.1"

// buildAnalysisProbeExecutables compiles the real Go helper for this test
// platform and writes it under every managed executable name the platform
// expects. The worker therefore runs a genuine native executable on Linux, macOS
// and Windows - no shell script and no cross-build - and the lifecycle's
// executable-suffix handling is exercised for real.
func buildAnalysisProbeExecutables(t *testing.T, toolsRoot string) string {
	t.Helper()
	directory := filepath.Join(toolsRoot, "ffmpeg", analysisDispatchRelease)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create the managed ffmpeg directory: %v", err)
	}
	names := tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS)
	target := filepath.Join(directory, names[0])
	build := exec.Command("go", "build", "-o", target, "./testdata/analysisprobe")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build the analysis probe helper: %v", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read the analysis probe helper: %v", err)
	}
	for _, name := range names[1:] {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o755); err != nil {
			t.Fatalf("write the managed %s: %v", name, err)
		}
	}
	return target
}

// analysisProbeExecutableName returns the platform name of one managed
// executable, including the suffix the platform adds.
func analysisProbeExecutableName(t *testing.T, want string) string {
	t.Helper()
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS) {
		if strings.TrimSuffix(name, filepath.Ext(name)) == want {
			return name
		}
	}
	t.Fatalf("the managed ffmpeg package has no %s executable for %s", want, runtime.GOOS)
	return ""
}

// writeAnalysisDispatchFFmpeg materializes the portable helper as the managed
// ffmpeg package and records and activates its installation. It returns the
// installation id and the built executable used to restore one removed later.
func writeAnalysisDispatchFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SettingsRepository, toolsRoot string, platform settings.PlatformState) (uuid.UUID, string) {
	t.Helper()
	helper := buildAnalysisProbeExecutables(t, toolsRoot)
	versions, err := json.Marshal(map[string]string{"ffmpeg": "ffmpeg version " + analysisDispatchRelease, "ffprobe": "ffprobe version " + analysisDispatchRelease})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFFmpeg),
		PlatformGOOS: platform.Platform.GOOS, PlatformGOARCH: platform.Platform.GOARCH,
		SourceName: "analysis-dispatch-test", ReleaseIdentity: analysisDispatchRelease,
		RelativePath: filepath.Join("ffmpeg", analysisDispatchRelease),
		State:        "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
	if err := persistence.NewSetupManagerRepository(database).CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create the managed ffmpeg installation: %v", err)
	}
	if err := repository.Set(ctx, settings.ActiveFFmpegInstallationKey, installation.ID.String()); err != nil {
		t.Fatalf("activate the managed ffmpeg installation: %v", err)
	}
	return installation.ID, helper
}

// installFailingAnalysisApplyTrigger fails the terminal running -> succeeded
// update of an analysis after the variant insert and the location link already ran
// in the same transaction, so the apply-failure test proves the whole apply rolls
// back.
func installFailingAnalysisApplyTrigger(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `
		CREATE FUNCTION fail_analysis_apply_dispatch() RETURNS trigger AS $$
		BEGIN
			IF OLD.state IN ('queued', 'running') AND NEW.state = 'succeeded' AND NEW.kind = 'analyze_source' THEN
				RAISE EXCEPTION 'injected analysis apply failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create the analysis apply trigger function: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TRIGGER fail_analysis_apply_dispatch
		BEFORE UPDATE ON operation
		FOR EACH ROW EXECUTE FUNCTION fail_analysis_apply_dispatch()`); err != nil {
		t.Fatalf("install the analysis apply trigger: %v", err)
	}
}

func dropFailingAnalysisApplyTrigger(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `DROP TRIGGER IF EXISTS fail_analysis_apply_dispatch ON operation`); err != nil {
		t.Fatalf("drop the analysis apply trigger: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DROP FUNCTION IF EXISTS fail_analysis_apply_dispatch()`); err != nil {
		t.Fatalf("drop the analysis apply trigger function: %v", err)
	}
}
