package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// OperationWithCleanupItems is a consistent read of an operation and its
// source-analysis artifact cleanup outcomes.
type OperationWithCleanupItems struct {
	Operation    *Operation
	CleanupItems []SourceAnalysisArtifactCleanupItem
}

// ReadOperationWithCleanupItems reads both operation state and cleanup items
// from one read-only, repeatable-read snapshot.
func (repository *SetupManagerRepository) ReadOperationWithCleanupItems(ctx context.Context, id uuid.UUID) (*OperationWithCleanupItems, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("read operation cleanup detail: operation is required")
	}

	detail := &OperationWithCleanupItems{}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		operation := new(Operation)
		if err := tx.NewSelect().Model(operation).Where("id = ?", id).Scan(ctx); err != nil {
			return fmt.Errorf("read operation: %w", err)
		}
		items := make([]SourceAnalysisArtifactCleanupItem, 0)
		if err := tx.NewRaw(`SELECT artifact_id,operation_id,operation_attempt,job_id,relative_output_path,state,safe_error
			FROM source_analysis_artifact_cleanup_item WHERE operation_id=? ORDER BY artifact_id`, id).Scan(ctx, &items); err != nil {
			return fmt.Errorf("read cleanup items: %w", err)
		}
		detail.Operation = operation
		detail.CleanupItems = items
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read operation cleanup detail: %w", err)
	}
	return detail, nil
}
