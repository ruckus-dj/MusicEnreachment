package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceScanUnavailable names a scan that found its registered directory
// unreadable, together with the safe reason to record on the root. The reason is
// what the UI shows instead of a raw diagnostic, so it must not be empty.
type SourceScanUnavailable struct {
	OperationID uuid.UUID
	SafeError   string
}

// SourceRootUnavailable names a rejected scan start that proved the registered
// directory of a root inaccessible, together with the safe reason to record on
// the root. A start is refused before its operation exists, so the root is named
// directly and the write is guarded by the root values the rejected attempt
// observed before it checked the directory.
//
// ExpectedConfiguredPath and ExpectedLastSuccess are that observation: the
// write is refused as a no-op when either changed, so an edit that moved the
// root to another path or a successful scan that landed after the observation is
// never overwritten by the older, superseded result.
type SourceRootUnavailable struct {
	RootID                 uuid.UUID
	SafeError              string
	ExpectedConfiguredPath string
	ExpectedLastSuccess    *time.Time
}

// MarkSourceRootUnavailable records that the registered directory of a scan is
// currently unreadable, so the UI can show the last successful inventory
// together with a truthful reason. Only status, safe_error and updated_at change:
// scan_generation, inventory_path, last_successful_scan_at,
// last_applied_operation_id and every location stay exactly as the last
// successful scan left them, because an unavailable root keeps its inventory.
//
// The write is refused as a no-op when the operation's current attempt began
// before the last successful scan of the root: a report from a scan a newer
// generation already superseded must not overwrite the availability that
// generation established. The attempt origin must not move when the operation
// later reaches a terminal state, so it is taken from the earliest durable
// record of the current attempt: started_at when the attempt ran, otherwise
// created_at for a first attempt and updated_at for a retry, which the retry
// transaction writes while it increments attempt. The root row is locked for the
// check and the write, so this mark and a successful apply serialize on the row
// and the later writer decides the final state.
func (repository *SourceInventoryRepository) MarkSourceRootUnavailable(ctx context.Context, unavailable SourceScanUnavailable) error {
	if unavailable.SafeError == "" {
		return fmt.Errorf("mark source root unavailable: a safe error is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation := new(Operation)
		if err := tx.NewSelect().Model(operation).
			Where("id = ?", unavailable.OperationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("mark source root unavailable: operation does not exist")
			}
			return fmt.Errorf("mark source root unavailable: read operation: %w", err)
		}
		if operation.TargetSourceRootID == nil {
			return fmt.Errorf("mark source root unavailable: operation does not target a source root")
		}
		attemptStartedAt := operation.CreatedAt
		if operation.Attempt > 1 {
			attemptStartedAt = operation.UpdatedAt
		}
		if operation.StartedAt != nil {
			attemptStartedAt = *operation.StartedAt
		}
		return markSourceRootUnavailable(ctx, tx, *operation.TargetSourceRootID, unavailable.SafeError, func(root *SourceRoot) bool {
			return root.LastSuccessfulScanAt != nil && !attemptStartedAt.After(*root.LastSuccessfulScanAt)
		})
	})
}

// MarkSourceRootUnavailableForRoot records the unavailable state for a scan of a
// root refused before its operation existed. The rejected attempt passes the
// root values it observed while it checked the directory; the write is refused
// as a no-op when the stored root no longer matches them, so a later edit or a
// successful scan of the root is never overwritten by the older observation.
func (repository *SourceInventoryRepository) MarkSourceRootUnavailableForRoot(ctx context.Context, unavailable SourceRootUnavailable) error {
	if unavailable.SafeError == "" {
		return fmt.Errorf("mark source root unavailable: a safe error is required")
	}
	if unavailable.RootID == uuid.Nil {
		return fmt.Errorf("mark source root unavailable: a source root is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		return markSourceRootUnavailable(ctx, tx, unavailable.RootID, unavailable.SafeError, func(root *SourceRoot) bool {
			return root.ConfiguredPath != unavailable.ExpectedConfiguredPath ||
				!sameSourceScanSuccess(root.LastSuccessfulScanAt, unavailable.ExpectedLastSuccess)
		})
	})
}

// markSourceRootUnavailable locks the root row and applies the update that only
// touches status, safe_error and updated_at. refuse is evaluated on the locked
// row, so a caller's pre-lock observation is compared against the state the
// write would replace; a true result leaves the root exactly as it is.
func markSourceRootUnavailable(ctx context.Context, tx bun.Tx, rootID uuid.UUID, safe string, refuse func(*SourceRoot) bool) error {
	root := new(SourceRoot)
	if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", rootID).Scan(ctx, root); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("mark source root unavailable: source root no longer exists")
		}
		return fmt.Errorf("mark source root unavailable: lock source root: %w", err)
	}
	if refuse(root) {
		return nil
	}
	if _, err := tx.NewUpdate().Model((*SourceRoot)(nil)).
		Set("status = ?", SourceRootStatusUnavailable).
		Set("safe_error = ?", safe).
		Set("updated_at = now()").
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		return fmt.Errorf("mark source root unavailable: %w", err)
	}
	return nil
}

// sameSourceScanSuccess reports whether two last_successful_scan_at readings
// describe the same successful generation. A root that was never scanned
// carries no timestamp, so a reading that gained one means a success landed
// after the observation.
func sameSourceScanSuccess(stored, observed *time.Time) bool {
	if stored == nil || observed == nil {
		return stored == nil && observed == nil
	}
	return stored.Equal(*observed)
}
