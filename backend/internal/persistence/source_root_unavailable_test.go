package persistence_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSourceRootUnavailableForRootRequiresAReasonAndARoot(t *testing.T) {
	t.Parallel()
	const configuredPath = "/srv/start-boundary"
	tests := []struct {
		name        string
		unavailable persistence.SourceRootUnavailable
		want        string
	}{
		{
			name:        "missing reason",
			unavailable: persistence.SourceRootUnavailable{RootID: uuid.New(), ExpectedConfiguredPath: configuredPath},
			want:        "a safe error is required",
		},
		{
			name:        "empty reason",
			unavailable: persistence.SourceRootUnavailable{RootID: uuid.New(), SafeError: "", ExpectedConfiguredPath: configuredPath},
			want:        "a safe error is required",
		},
		{
			name:        "missing root",
			unavailable: persistence.SourceRootUnavailable{SafeError: "The configured source directory is unavailable.", ExpectedConfiguredPath: configuredPath},
			want:        "a source root is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := new(persistence.SourceInventoryRepository).MarkSourceRootUnavailableForRoot(context.Background(), test.unavailable)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mark %+v err=%v, want %q", test.unavailable, err, test.want)
			}
		})
	}
}
