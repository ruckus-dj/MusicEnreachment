package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ReleaseMatchingApplyDelivery makes a failed execution immediately retryable
// without releasing a newer delivery's claim.
func (repository *SetupManagerRepository) ReleaseMatchingApplyDelivery(ctx context.Context, operationID uuid.UUID, attempt int, jobID int64, epoch int64) error {
	_, err := repository.db.NewRaw(`UPDATE operation SET provider_delivery_claimed_at=NULL
		WHERE id=? AND kind=? AND attempt=? AND river_job_id=? AND provider_execution_epoch=?
		AND state IN ('queued','running')`, operationID, MatchingApplyOperationKind, attempt, jobID, epoch).Exec(ctx)
	if err != nil {
		return fmt.Errorf("release matching apply delivery: %w", err)
	}
	return nil
}
