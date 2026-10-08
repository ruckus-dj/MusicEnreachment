package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// TestWorkerSafeReasonsClassifyPlatformErrors pins analysis-worker platform
// validation and keeps an unsupported source root distinct and actionable. Scan
// enumeration does not depend on the instance's managed analysis tools.
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
	for name, reason := range map[string]string{"analysis": analysisSafeNotReady} {
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
