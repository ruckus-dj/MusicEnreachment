//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func assertIncomingReadiness(t *testing.T, ctx context.Context, repository *persistence.IncomingGroupingRepository, workID uuid.UUID, want bool) {
	t.Helper()
	captures, err := repository.CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read incoming captures: %v", err)
	}
	for _, capture := range captures {
		if capture.WorkID == workID && capture.Ready == want {
			return
		}
	}
	t.Fatalf("work %v readiness not found as %v in captures %+v", workID, want, captures)
}

func nullableTestString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
