package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// sourceScanOperationKind is the stored kind of a scan of a source root. The
// service layer names it, but it imports this package, so the value the schema
// constrains is repeated here next to the enqueue of a scan start.
const sourceScanOperationKind = "scan_source"

// RetrySourceScanOperationAndEnqueue re-runs one failed scan of a source root as
// a new complete traversal under the same operation id: the candidates of the
// failed attempt are dropped and the next traversal job is inserted in one
// transaction, so a retry that cannot be delivered leaves the operation failed
// with its candidates intact and its immutable snapshot preserved.
//
// The transaction takes the operation table lock before it locks the source root
// row, which is the order a scan start, a root edit and a root deletion use, so a
// retry cannot resurrect a scan against a root another writer removed, nor start
// while another scan of the same root is queued or running. Under that lock the
// method refuses a disabled root with ErrSourceRootDisabled and a root with an
// active scan with ErrSourceRootActiveScan; an operation that is not a failed
// scan of a source root is refused as well.
func (repository *SetupManagerRepository) RetrySourceScanOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts) (*Operation, error) {
	if client == nil {
		return nil, fmt.Errorf("retry source scan: River client is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("retry source scan: lock operations: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Kind != sourceScanOperationKind || locked.TargetSourceRootID == nil {
			return fmt.Errorf("retry source scan: operation is not a scan of a source root")
		}
		if locked.State != "failed" {
			return fmt.Errorf("retry source scan: only a failed source scan can be retried")
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", *locked.TargetSourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("retry source scan: the source root no longer exists")
			}
			return fmt.Errorf("retry source scan: lock source root: %w", err)
		}
		if !root.Enabled {
			return fmt.Errorf("retry source scan: the root %q is disabled: %w", root.DisplayName, ErrSourceRootDisabled)
		}
		active, err := activeSourceScan(ctx, tx, root.ID)
		if err != nil {
			return fmt.Errorf("retry source scan: check the active scan of the root: %w", err)
		}
		if active {
			return fmt.Errorf("retry source scan: %w", ErrSourceRootActiveScan)
		}
		// Whatever the failed attempt left behind goes before the job that walks
		// the tree again, so no candidate of the failed attempt can be applied.
		if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).
			Where("operation_id = ?", locked.ID).Exec(ctx); err != nil {
			return fmt.Errorf("retry source scan: drop the candidates of the failed attempt: %w", err)
		}
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("retry source scan: insert River job: %w", err)
		}
		locked.State = "queued"
		if !strings.HasPrefix(locked.Stage, "retry:") {
			locked.Stage = "retry:" + locked.Stage
		}
		locked.SafeError = nil
		locked.StartedAt = nil
		locked.FinishedAt = nil
		locked.BytesCompleted = 0
		locked.BytesTotal = nil
		locked.RiverJobID = &result.Job.ID
		locked.Attempt++
		locked.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(locked).
			Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry source scan: %w", err)
		}
		operation = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}
