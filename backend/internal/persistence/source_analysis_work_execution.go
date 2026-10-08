package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceAnalysisWorkExecution is the immutable mode and delivery identity
// captured when one work item starts executing.
type SourceAnalysisWorkExecution struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	ProcessingMode   string
}

// GetSourceAnalysisWorkExecution returns the mode selected for the exact
// delivery. It does not consult the mutable source-root mode.
func (repository *SourceInventoryRepository) GetSourceAnalysisWorkExecution(ctx context.Context, fence SourceAnalysisArtifactFence) (*SourceAnalysisWorkExecution, error) {
	if !validSourceAnalysisArtifactFence(fence) {
		return nil, fmt.Errorf("get source analysis work execution: valid delivery fence is required")
	}
	execution := new(SourceAnalysisWorkExecution)
	err := repository.db.NewRaw(`SELECT work_id, operation_id, operation_attempt, job_id, processing_mode
		FROM source_analysis_work_execution
		WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?`,
		fence.WorkID, fence.OperationID, fence.OperationAttempt, fence.JobID).Scan(ctx, execution)
	if err != nil {
		return nil, fmt.Errorf("get source analysis work execution: %w", err)
	}
	return execution, nil
}

func getSourceAnalysisWorkExecution(ctx context.Context, tx bun.IDB, fence SourceAnalysisArtifactFence) (*SourceAnalysisWorkExecution, error) {
	execution := new(SourceAnalysisWorkExecution)
	err := tx.NewRaw(`SELECT work_id, operation_id, operation_attempt, job_id, processing_mode
		FROM source_analysis_work_execution
		WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?`,
		fence.WorkID, fence.OperationID, fence.OperationAttempt, fence.JobID).Scan(ctx, execution)
	if err != nil {
		return nil, fmt.Errorf("read source analysis work execution: %w", err)
	}
	return execution, nil
}
