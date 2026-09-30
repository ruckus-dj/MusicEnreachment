package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// RecoverInterruptedSourceScan resolves one scan operation whose River delivery
// is gone. It reports whether the generation of that operation was already
// applied to its root: the apply is deliberately not atomic with the operation
// transition, so a process that stopped between the two leaves the operation
// active while the root already records it as the last applied generation. Such
// an operation is only finished, never applied again.
//
// The private candidates of an operation that was not applied are dropped in the
// same transaction, so an orphaned scan leaves nothing to apply later and the
// previous inventory stays exactly as the last successful scan left it. Recovery
// never writes a location, so no outcome can change the inventory.
//
// The operation table is locked before the source root row, the order a scan
// start, a root edit and a root deletion use, so the decision is never taken
// against a root another writer is changing.
func (repository *SetupManagerRepository) RecoverInterruptedSourceScan(ctx context.Context, operationID uuid.UUID) (bool, error) {
	applied := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("recover source scan: lock operations: %w", err)
		}
		var rootID *uuid.UUID
		if err := tx.NewRaw("SELECT target_source_root_id FROM operation WHERE id = ?", operationID).Scan(ctx, &rootID); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("recover source scan: operation %s does not exist", operationID)
			}
			return fmt.Errorf("recover source scan: read the operation target: %w", err)
		}
		if rootID == nil {
			// The root row is gone and took the reference with it, so the scan
			// cannot be resolved against a root: only its candidates are left.
			return deleteSourceScanCandidates(ctx, tx, operationID)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", *rootID).Scan(ctx, root); err != nil {
			if err != sql.ErrNoRows {
				return fmt.Errorf("recover source scan: lock the source root: %w", err)
			}
			// A root that is gone takes its locations with it, but the candidates
			// belong to the operation and must not outlive its recovery.
			return deleteSourceScanCandidates(ctx, tx, operationID)
		}
		applied = root.LastAppliedOperationID != nil && *root.LastAppliedOperationID == operationID
		if applied {
			return nil
		}
		return deleteSourceScanCandidates(ctx, tx, operationID)
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}
