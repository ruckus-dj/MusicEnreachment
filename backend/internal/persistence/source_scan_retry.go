package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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

type sourceScanSnapshot struct {
	SchemaVersion int       `json:"schema_version"`
	SourceRootID  uuid.UUID `json:"source_root_id"`
}

func decodeSourceScanSnapshot(input json.RawMessage) (sourceScanSnapshot, error) {
	var snapshot sourceScanSnapshot
	if err := json.Unmarshal(input, &snapshot); err != nil ||
		snapshot.SchemaVersion != 3 || snapshot.SourceRootID == uuid.Nil {
		return sourceScanSnapshot{}, fmt.Errorf("invalid immutable snapshot")
	}
	return snapshot, nil
}

func sameScanJobID(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func scanOperationMatchesCapture(captured, locked *Operation, rootID uuid.UUID) bool {
	return locked.ID == captured.ID && locked.Kind == captured.Kind && locked.State == captured.State &&
		locked.Attempt == captured.Attempt && sameScanJobID(locked.RiverJobID, captured.RiverJobID) &&
		bytes.Equal(locked.InputSnapshot, captured.InputSnapshot) &&
		(captured.TargetSourceRootID == nil || *captured.TargetSourceRootID == rootID) &&
		(locked.TargetSourceRootID == nil || *locked.TargetSourceRootID == rootID)
}

// RetrySourceScanOperationAndEnqueue re-runs one failed scan of a source root as
// a new complete traversal under the same operation id: the candidates of the
// failed attempt are dropped and the next traversal job is inserted in one
// transaction, so a retry that cannot be delivered leaves the operation failed
// with its candidates intact and its immutable snapshot preserved.
//
// The transaction reads the operation and its immutable snapshot without a lock,
// then locks the root before locking and revalidating the operation. The root lock
// serializes the path/generation check with edits and scan application; the
// active-scan check prevents a retry from racing a new traversal. Disabled roots
// and operations that are not failed scans are refused as well.
func (repository *SetupManagerRepository) RetrySourceScanOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter, args river.JobArgs, options *river.InsertOpts) (*Operation, error) {
	if client == nil {
		return nil, fmt.Errorf("retry source scan: River client is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retry source scan: lock output admission gate: %w", err)
		}
		captured := new(Operation)
		if err := tx.NewSelect().Model(captured).Where("id = ?", id).Scan(ctx); err != nil {
			return fmt.Errorf("retry source scan: read operation: %w", err)
		}
		if captured.Kind != sourceScanOperationKind {
			return fmt.Errorf("retry source scan: operation is not a scan of a source root")
		}
		if captured.State != "failed" {
			return fmt.Errorf("retry source scan: only a failed source scan can be retried")
		}
		snapshot, err := decodeSourceScanSnapshot(captured.InputSnapshot)
		if err != nil {
			return fmt.Errorf("retry source scan: operation has an invalid immutable snapshot")
		}
		rootID := snapshot.SourceRootID
		if captured.TargetSourceRootID != nil && *captured.TargetSourceRootID != rootID {
			return fmt.Errorf("retry source scan: root target does not match its immutable snapshot")
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", rootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("retry source scan: the source root no longer exists")
			}
			return fmt.Errorf("retry source scan: lock source root: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if !scanOperationMatchesCapture(captured, locked, rootID) {
			return fmt.Errorf("retry source scan: operation changed while acquiring the source root lock")
		}
		minimalSnapshot, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("retry source scan: encode minimal snapshot: %w", err)
		}
		locked.InputSnapshot = minimalSnapshot
		locked.TargetSourceRootID = &root.ID
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
			Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at", "target_source_root_id", "input_snapshot").
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
