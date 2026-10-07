package persistence

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// lockToolsMoveExclusive excludes all tools-root readers while a move runs.
func lockToolsMoveExclusive(ctx context.Context, tx bun.IDB) error {
	return advisoryXactLock(ctx, tx, false, toolsCoordinationNamespace, toolsMoveGateKey)
}

// lockPackageActivations serializes activation against readers selecting the
// same package kinds. Multiple kinds are acquired in sorted order.
func lockPackageActivations(ctx context.Context, tx bun.IDB, packageKinds []string) error {
	return lockPackageKinds(ctx, tx, packageKinds, false)
}

// lockToolsRootsReaders locks existing source-root rows for shared access.
// Independent roots do not block one another, and readers of the same root can
// coexist. Row identity, rather than a truncated path hash, is the lock key.
// Every requested root must exist: a missing path is an error, never a silent
// partial lock.
func lockToolsRootsReaders(ctx context.Context, tx bun.IDB, roots []string) error {
	return lockToolsRoots(ctx, tx, roots, true)
}

func lockToolsRoots(ctx context.Context, tx bun.IDB, roots []string, shared bool) error {
	ordered := append([]string(nil), roots...)
	sort.Strings(ordered)
	uniqueRoots := ordered[:0]
	for _, root := range ordered {
		if len(uniqueRoots) == 0 || uniqueRoots[len(uniqueRoots)-1] != root {
			uniqueRoots = append(uniqueRoots, root)
		}
	}
	if len(uniqueRoots) == 0 {
		return nil
	}
	lockClause := "FOR SHARE"
	if !shared {
		lockClause = "FOR UPDATE"
	}
	type lockedRoot struct {
		ID             uuid.UUID `bun:"id,type:uuid"`
		ConfiguredPath string    `bun:"configured_path"`
	}
	locked := make([]lockedRoot, 0, len(uniqueRoots))
	query := "SELECT id, configured_path FROM source_root WHERE configured_path IN (?) ORDER BY id " + lockClause
	if err := tx.NewRaw(query, bun.List(uniqueRoots)).Scan(ctx, &locked); err != nil {
		return fmt.Errorf("lock tools roots: %w", err)
	}
	if len(locked) != len(uniqueRoots) {
		lockedPaths := make(map[string]struct{}, len(locked))
		for _, root := range locked {
			lockedPaths[root.ConfiguredPath] = struct{}{}
		}
		missing := make([]string, 0, len(uniqueRoots)-len(locked))
		for _, root := range uniqueRoots {
			if _, ok := lockedPaths[root]; !ok {
				missing = append(missing, root)
			}
		}
		return fmt.Errorf("lock tools roots: source roots are not registered: %s", strings.Join(missing, ", "))
	}
	return nil
}
