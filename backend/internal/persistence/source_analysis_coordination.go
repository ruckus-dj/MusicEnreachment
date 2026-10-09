package persistence

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Coordination advisory-lock keys use a fixed namespace in the first int4 and
// stable, domain-specific values in the second. Do not use hashtext: its key
// space can overlap unrelated advisory-lock users.
const (
	toolsCoordinationNamespace       int32 = 0x4d545256 // "MTRV"
	toolsMoveGateKey                 int32 = 1
	setupCompletionLockKey           int32 = 2
	sourceFingerprintMutationGateKey int32 = 4
	toolsPackageLockBase             int32 = 100
)

// lockSourceFingerprintMutations serializes transactions that can publish,
// select, or retire digest-keyed fingerprint and metadata results. Acquire it before any
// root, location, work, operation, or step row lock; callers of lower-level
// promotion/retirement helpers are responsible for holding this gate.
func lockSourceFingerprintMutations(ctx context.Context, database bun.IDB) error {
	return advisoryXactLock(ctx, database, false, toolsCoordinationNamespace, sourceFingerprintMutationGateKey)
}

// lockToolsMoveReaders admits operations that only read/use the configured
// tools root. A root move takes the matching exclusive gate for its duration.
func lockToolsMoveReaders(ctx context.Context, tx bun.IDB) error {
	return advisoryXactLock(ctx, tx, true, toolsCoordinationNamespace, toolsMoveGateKey)
}

// activeToolsMoveExists is a plain MVCC read and must be called after taking
// the shared tools-move gate. It deliberately does not row-lock operation:
// concurrent readers admitted by the shared gate must not serialize there.
func activeToolsMoveExists(ctx context.Context, tx bun.IDB) (bool, error) {
	var exists bool
	err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM operation
		WHERE kind = 'move_tools_root' AND state IN ('queued', 'running')
	)`).Scan(ctx, &exists)
	return exists, err
}

// lockPackageSelections takes shared locks for readers selecting active
// packages. Package kinds are validated and acquired in a stable order.
func lockPackageSelections(ctx context.Context, tx bun.IDB, packageKinds []string) error {
	return lockPackageKinds(ctx, tx, packageKinds, true)
}

func lockPackageKinds(ctx context.Context, tx bun.IDB, packageKinds []string, shared bool) error {
	keys := make(map[string]int32, len(packageKinds))
	for _, kind := range packageKinds {
		switch kind {
		case "ffmpeg":
			keys[kind] = toolsPackageLockBase + 1
		case "fpcalc":
			keys[kind] = toolsPackageLockBase + 2
		default:
			return fmt.Errorf("unsupported package kind %q", kind)
		}
	}
	kinds := make([]string, 0, len(keys))
	for kind := range keys {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		if err := advisoryXactLock(ctx, tx, shared, toolsCoordinationNamespace, keys[kind]); err != nil {
			return fmt.Errorf("lock package selection %s: %w", kind, err)
		}
	}
	return nil
}

// lockCompatibleInstallations holds compatible installation rows against
// concurrent update or deletion. Individual row locks are acquired by UUID
// order; FOR SHARE intentionally permits other readers to proceed together.
func lockCompatibleInstallations(ctx context.Context, tx bun.IDB, installationIDs []uuid.UUID) error {
	ordered := append([]uuid.UUID(nil), installationIDs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	var previous uuid.UUID
	havePrevious := false
	for _, id := range ordered {
		if havePrevious && id == previous {
			continue
		}
		var lockedID uuid.UUID
		if err := tx.NewRaw("SELECT id FROM tool_installation WHERE id = ? FOR SHARE", id).Scan(ctx, &lockedID); err != nil {
			return fmt.Errorf("lock compatible installation %s: %w", id, err)
		}
		previous, havePrevious = id, true
	}
	return nil
}

func advisoryXactLock(ctx context.Context, tx bun.IDB, shared bool, namespace, key int32) error {
	function := "pg_advisory_xact_lock"
	if shared {
		function = "pg_advisory_xact_lock_shared"
	}
	_, err := tx.NewRaw("SELECT "+function+"(?, ?)", namespace, key).Exec(ctx)
	return err
}
