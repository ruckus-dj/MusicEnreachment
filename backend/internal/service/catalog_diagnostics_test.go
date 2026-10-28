package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

func TestToolCatalogErrorMessageOnlyExposesSafeRateDiagnostic(t *testing.T) {
	const secret = "secret-from-transport"
	if got := ToolCatalogErrorMessage(fmt.Errorf("request failed: %s", secret)); got != "upstream tool catalog is unavailable" {
		t.Fatalf("unclassified error exposed: %s", got)
	}
	err := fmt.Errorf("%s: %w", secret, &tools.GitHubRateLimitError{RetryAfter: "rate limit resets at 2030-01-01T00:00:00Z"})
	got := ToolCatalogErrorMessage(err)
	if !strings.Contains(got, "2030-01-01T00:00:00Z") || strings.Contains(got, secret) {
		t.Fatalf("unsafe or missing rate diagnostic: %s", got)
	}
}
