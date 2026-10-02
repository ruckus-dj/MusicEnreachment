package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FailSourceAnalysisOperation records the safe reason on one running analysis and
// releases both of its read holds in the same transaction. A terminal analysis
// must never keep a hold: the previous variant it kept alive may become an
// orphan and is removed by the same cleanup the successful apply runs, and the
// pinned installation stops being held so it can be deleted or moved again.
//
// The operation row is locked before the write, and a duplicate failure of an
// operation that is already terminal is a no-op, so a second delivery cannot
// rewrite a settled state.
func (repository *SourceInventoryRepository) FailSourceAnalysisOperation(ctx context.Context, id uuid.UUID, stage, safe string) error {
	if safe == "" {
		return fmt.Errorf("fail source analysis: a safe error is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// The shared operation table lock comes first, the order the apply,
		// start, retry and every root/tool mutation use. Locking the operation row
		// before it would let the terminal UPDATE wait for the table ROW EXCLUSIVE
		// while a root mutation holds SHARE ROW EXCLUSIVE and waits for this row.
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("fail source analysis: lock operations: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", id).Scan(ctx, operation); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("fail source analysis: operation does not exist")
			}
			return fmt.Errorf("fail source analysis: lock operation: %w", err)
		}
		if operation.Kind != analysisSourceOperationKind {
			return fmt.Errorf("fail source analysis: operation is not a source analysis")
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return nil
		}
		now := time.Now().UTC()
		operation.State = "failed"
		operation.Stage = stage
		operation.SafeError = &safe
		operation.FinishedAt = &now
		operation.AnalysisInstallationID = nil
		operation.AnalysisMediaVariantID = nil
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).
			Column("state", "stage", "safe_error", "analysis_installation_id", "analysis_media_variant_id", "finished_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("fail source analysis: %w", err)
		}
		if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
			return fmt.Errorf("fail source analysis: %w", err)
		}
		return nil
	})
}

// RecoverInterruptedSourceAnalysis resolves one analysis whose River delivery is
// gone. The successful apply commits the new variant, the location link and the
// succeeded state in one transaction, so an operation still queued or running
// cannot have published a result: it never applied. Recovery therefore fails it
// with the caller's safe reason and releases both read holds in the same
// transaction, so a terminal operation never keeps a variant or an installation
// hold. An operation that already succeeded is not in the reconcile set and is
// never touched here; there is no half-committed state to reconcile.
//
// The operation table is locked before the row, the order a start, a root
// deletion and this recovery use.
func (repository *SetupManagerRepository) RecoverInterruptedSourceAnalysis(ctx context.Context, operationID uuid.UUID, safeError string) error {
	if safeError == "" {
		return fmt.Errorf("recover source analysis: a safe error is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("recover source analysis: lock operations: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", operationID).Scan(ctx, operation); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("recover source analysis: operation %s does not exist", operationID)
			}
			return fmt.Errorf("recover source analysis: lock operation: %w", err)
		}
		if operation.Kind != analysisSourceOperationKind {
			return fmt.Errorf("recover source analysis: operation is not a source analysis")
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return nil
		}
		now := time.Now().UTC()
		operation.State = "failed"
		operation.SafeError = &safeError
		operation.FinishedAt = &now
		operation.AnalysisInstallationID = nil
		operation.AnalysisMediaVariantID = nil
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).
			Column("state", "stage", "safe_error", "analysis_installation_id", "analysis_media_variant_id", "finished_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("recover source analysis: %w", err)
		}
		if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
			return fmt.Errorf("recover source analysis: %w", err)
		}
		return nil
	})
}
