package jobs

import (
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestUnsupportedSourceRootHasActionableOperationReasons(t *testing.T) {
	if got := analysisRunSafe(service.ErrUnsupportedSourceRoot); got != analysisSafeUnsupported {
		t.Fatalf("analysisRunSafe(unsupported root) = %q, want %q", got, analysisSafeUnsupported)
	}
	if scanSafeUnsupported == scanSafeTraversal || scanSafeUnsupported == scanSafePath {
		t.Fatalf("unsupported scan reason is not distinct: %q", scanSafeUnsupported)
	}
	for name, message := range map[string]string{
		"scan": scanSafeUnsupported, "analysis": analysisSafeUnsupported,
	} {
		if !strings.Contains(message, "not supported") || !strings.Contains(message, "local drive path") {
			t.Errorf("%s unsupported-root reason = %q, want actionable local-path guidance", name, message)
		}
	}
}
