package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// TestWorkerSafeReasonsClassifyPlatformErrors pins the current worker's safe
// platform-error classification: an instance platform that cannot run work is
// refused at the worker's platform gate and reported with the actionable
// not-ready reason, never as an unsupported source root. The unsupported-root
// reason stays separate and actionable so an operator sees the local-path fix.
func TestWorkerSafeReasonsClassifyPlatformErrors(t *testing.T) {
	ctx := context.Background()
	unusable := settings.PlatformState{
		Platform:   settings.Platform{GOOS: "windows", GOARCH: "arm64"},
		Diagnostic: true,
		Reason:     "unsupported platform",
	}

	// A platform the worker cannot use is refused before any source is touched.
	if err := (&SourceAnalysisWorker{platform: unusable}).ready(ctx); err == nil {
		t.Fatal("analysis worker accepted an unusable platform")
	}
	if err := (&SourceScanWorker{platform: unusable}).ready(ctx); err == nil {
		t.Fatal("scan worker accepted an unusable platform")
	}

	for name, reason := range map[string]string{"scan": scanSafeNotReady, "analysis": analysisSafeNotReady} {
		if !strings.Contains(reason, "supported server platform") {
			t.Errorf("%s not-ready reason = %q, want actionable platform guidance", name, reason)
		}
	}

	if scanSafeUnsupported == scanSafeTraversal || scanSafeUnsupported == scanSafePath {
		t.Fatalf("unsupported scan reason is not distinct: %q", scanSafeUnsupported)
	}
	if !strings.Contains(scanSafeUnsupported, "not supported") || !strings.Contains(scanSafeUnsupported, "local drive path") {
		t.Errorf("unsupported scan reason = %q, want actionable local-path guidance", scanSafeUnsupported)
	}
}
