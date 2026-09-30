package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// ErrSourceRootDisabled reports a scan start refused because the operator
// disabled the root. A disabled root keeps its previous inventory and is only
// traversed again after an edit enables it.
var ErrSourceRootDisabled = errors.New("source root is disabled")

// CreateSourceScanOperationAndEnqueue records a queued scan of a root and its
// River job in one transaction: neither record is visible if either insert
// fails, so no scan is ever left without the job that would run it and no job
// is ever left without its operation.
//
// The transaction takes the operation table lock before it locks the source
// root row, which is the order DeleteSourceRoot and UpdateSourceRoot use, so a
// scan cannot start on a root that is being deleted and a deletion cannot pass
// its check while a scan is being recorded. Holding that lock, the method
// refuses a disabled root with ErrSourceRootDisabled and a root whose scan is
// already queued or running with ErrSourceRootActiveScan, which serializes two
// concurrent starts of the same root. The partial unique index on active scans
// stays the durable backstop of that check.
func (repository *SourceInventoryRepository) CreateSourceScanOperationAndEnqueue(ctx context.Context, operation *Operation, client RiverInserter, args river.JobArgs, options *river.InsertOpts) error {
	if client == nil {
		return fmt.Errorf("enqueue source scan: River client is required")
	}
	if operation == nil || operation.Kind != "scan_source" || operation.TargetSourceRootID == nil {
		return fmt.Errorf("enqueue source scan: operation must be a scan of a source root")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("enqueue source scan: lock operations: %w", err)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", *operation.TargetSourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("enqueue source scan: the source root no longer exists")
			}
			return fmt.Errorf("enqueue source scan: lock source root: %w", err)
		}
		if !root.Enabled {
			return fmt.Errorf("enqueue source scan: the root %q is disabled: %w", root.DisplayName, ErrSourceRootDisabled)
		}
		active, err := activeSourceScan(ctx, tx, root.ID)
		if err != nil {
			return fmt.Errorf("enqueue source scan: check the active scan of the root: %w", err)
		}
		if active {
			return fmt.Errorf("enqueue source scan: %w", ErrSourceRootActiveScan)
		}
		// The job is inserted first so the operation row carries the id River
		// assigned to it; either insert failing rolls back both rows.
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("enqueue source scan: insert River job: %w", err)
		}
		operation.RiverJobID = &result.Job.ID
		if operation.Attempt == 0 {
			operation.Attempt = 1
		}
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return fmt.Errorf("enqueue source scan: create operation: %w", err)
		}
		return nil
	})
}
