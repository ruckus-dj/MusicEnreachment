//go:build integration

package persistence

import (
	"context"
	"io/fs"
	"testing"
)

func resolveAllRetryItems(t *testing.T, f retryResolutionFixture) {
	t.Helper()
	for i, itemID := range f.itemIDs {
		var remove func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error)
		switch i {
		case 1:
			remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
			}
		case 2:
			remove = func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
				return PublicationSourceCleanupNativeRemoved, nil
			}
		}
		got, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, itemID, remove)
		if err != nil || got != PublicationSourceCleanupRetryResolutionCompleted {
			t.Fatalf("resolve retry item %s = %s, %v", itemID, got, err)
		}
	}
}
