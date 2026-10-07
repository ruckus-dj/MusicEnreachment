package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// FailSourceAnalysisOperation fails a normalized analysis, releases every
// persisted hold, and clears all step delivery fences atomically.
func (repository *SourceInventoryRepository) FailSourceAnalysisOperation(ctx context.Context, id uuid.UUID, stage, safe string) error {
	return repository.SettleNormalizedSourceAnalysisOperation(ctx, id, "failed", stage, safe)
}

// RecoverInterruptedSourceAnalysis returns an orphaned delivery to pending work.
// It deliberately does not decode the operation snapshot: recovery cleans up
// durable holds and step fences even if a historical snapshot is malformed.
func (repository *SetupManagerRepository) RecoverInterruptedSourceAnalysis(ctx context.Context, operationID uuid.UUID, safeError string) error {
	if operationID == uuid.Nil || safeError == "" {
		return fmt.Errorf("recover source analysis: a safe error is required")
	}
	// Capture the delivery before taking locks in root/work/operation order. The
	// operation row is revalidated after those locks so a concurrent retry cannot
	// have its newer fences cleared by recovery of an older delivery.
	captured := new(Operation)
	if err := repository.db.NewRaw(`SELECT * FROM operation WHERE id=?`, operationID).Scan(ctx, captured); err != nil {
		return fmt.Errorf("recover source analysis: read operation: %w", err)
	}
	if captured.State == "succeeded" || captured.State == "failed" {
		return nil
	}
	if captured.SourceAnalysisMode == "" || captured.Attempt < 1 || captured.RiverJobID == nil {
		return fmt.Errorf("recover source analysis: operation is not a valid normalized delivery")
	}
	return recoverInterruptedNormalizedSourceAnalysis(ctx, repository.db, captured, SourceAnalysisOperationDelivery{Attempt: captured.Attempt, JobID: *captured.RiverJobID}, safeError, false)
}
