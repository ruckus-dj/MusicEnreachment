package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
// Recovery reads the immutable root selector without a lock, then locks the root
// before the operation, matching scan apply and delivery admission. It fences
// that unlocked read after both locks are held so a newer delivery is untouched.
func (repository *SetupManagerRepository) RecoverInterruptedSourceScan(ctx context.Context, operationID uuid.UUID) (bool, error) {
	applied := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		captured := new(Operation)
		if err := tx.NewSelect().Model(captured).Where("id = ?", operationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("recover source scan: operation %s does not exist", operationID)
			}
			return fmt.Errorf("recover source scan: read the operation: %w", err)
		}
		if captured.Kind != sourceScanOperationKind || captured.State != "queued" && captured.State != "running" {
			return fmt.Errorf("recover source scan: operation is not an active source scan")
		}
		snapshot, err := decodeSourceScanSnapshot(captured.InputSnapshot)
		if err != nil {
			return fmt.Errorf("recover source scan: operation has an invalid immutable snapshot")
		}
		rootID := snapshot.SourceRootID
		if captured.TargetSourceRootID != nil && *captured.TargetSourceRootID != rootID {
			return fmt.Errorf("recover source scan: root target does not match its immutable snapshot")
		}
		root := new(SourceRoot)
		rootErr := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", rootID).Scan(ctx, root)
		if rootErr != nil && rootErr != sql.ErrNoRows {
			return fmt.Errorf("recover source scan: lock the source root: %w", rootErr)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", operationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("recover source scan: lock operation: %w", err)
		}
		if !scanOperationMatchesCapture(captured, operation, rootID) {
			return fmt.Errorf("recover source scan: operation changed while acquiring the source root lock")
		}
		if rootErr == sql.ErrNoRows {
			// A deleted root takes its selector with it. Candidates still belong to
			// this fenced operation and must not outlive its recovery.
			return deleteSourceScanCandidates(ctx, tx, operationID)
		}
		// The apply commits before the operation settles. Recognize that durable
		// marker before considering the snapshot generation or deleting candidates.
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

// FinishRecoveredSourceScanWithMatchingReconcileEvent atomically settles an
// already-applied interrupted scan and records its successful-scan event. The
// root's applied-operation marker is the durable receipt written by scan apply.
func (repository *SetupManagerRepository) FinishRecoveredSourceScanWithMatchingReconcileEvent(ctx context.Context, operationID uuid.UUID, expectedAttempt int, stage string) (bool, error) {
	if operationID == uuid.Nil || expectedAttempt < 1 || stage == "" {
		return false, fmt.Errorf("finish recovered source scan: valid operation identity, attempt, and stage are required")
	}
	finished := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		captured := new(Operation)
		if err := tx.NewSelect().Model(captured).Where("id = ?", operationID).Scan(ctx); err != nil {
			return fmt.Errorf("finish recovered source scan: read operation: %w", err)
		}
		if captured.Kind != sourceScanOperationKind || captured.Attempt != expectedAttempt || captured.State != "queued" && captured.State != "running" {
			return fmt.Errorf("finish recovered source scan: operation is no longer the active expected attempt")
		}
		snapshot, err := decodeSourceScanSnapshot(captured.InputSnapshot)
		if err != nil || captured.TargetSourceRootID != nil && *captured.TargetSourceRootID != snapshot.SourceRootID {
			return fmt.Errorf("finish recovered source scan: immutable source root does not match the operation")
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", snapshot.SourceRootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("finish recovered source scan: lock source root: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", operationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("finish recovered source scan: lock operation: %w", err)
		}
		if !scanOperationMatchesCapture(captured, operation, snapshot.SourceRootID) || operation.Attempt != expectedAttempt {
			return fmt.Errorf("finish recovered source scan: operation changed while acquiring the source root lock")
		}
		if root.LastAppliedOperationID == nil || *root.LastAppliedOperationID != operationID {
			if err := deleteSourceScanCandidates(ctx, tx, operationID); err != nil {
				return fmt.Errorf("finish recovered source scan: delete unapplied candidates: %w", err)
			}
			return nil
		}
		var hasSuccessfulRetainedAnalysis bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_analysis_work work
			JOIN source_analysis_step step ON step.work_id=work.id AND step.state='succeeded'
			WHERE work.origin_scan_operation_id=?
		)`, operationID).Scan(ctx, &hasSuccessfulRetainedAnalysis); err != nil {
			return fmt.Errorf("finish recovered source scan: check retained successful analysis: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("finish recovered source scan: release tool holds: %w", err)
		}
		operation.ToolsReadRequired = false
		operation.State = "succeeded"
		operation.Stage = stage
		operation.SafeError = nil
		operation.FinishedAt = ptrTime(time.Now().UTC())
		operation.UpdatedAt = time.Now().UTC()
		operation.TargetWorkID = nil
		operation.TargetStep = nil
		operation.TargetSourceRootID = nil
		operation.TargetSourceLocationID = nil
		operation.RerunTarget = false
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at", "target_work_id", "target_step", "target_source_root_id", "target_source_location_id", "tools_read_required", "rerun_target").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("finish recovered source scan: settle operation: %w", err)
		}
		if hasSuccessfulRetainedAnalysis {
			if err := insertIncomingMatchingReconcileEvent(ctx, tx, operation, "scan_success"); err != nil {
				return fmt.Errorf("finish recovered source scan: persist matching reconciliation event: %w", err)
			}
		}
		finished = true
		return nil
	})
	return finished, err
}
