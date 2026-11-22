package persistence

import (
	"context"
	"fmt"
)

// RiverJobLiveness reports whether a stored River job id names a live delivery:
// its river_job row is still queued for a worker (available, pending, retryable
// or scheduled). A missing id and a job in a terminal state are not live.
//
// It lives in the persistence layer because it is the only layer that touches
// PostgreSQL; the composition root and the startup-recovery tests pass this
// bound method as the liveness callback, so no other layer issues this query.
func (repository *SetupManagerRepository) RiverJobLiveness(ctx context.Context, jobID *int64) (bool, error) {
	if jobID == nil {
		return false, nil
	}
	var live bool
	if err := repository.db.NewRaw(`
		SELECT EXISTS (
			SELECT 1 FROM river_job
			WHERE id = ? AND state IN ('available', 'pending', 'retryable', 'scheduled')
		)`, *jobID).Scan(ctx, &live); err != nil {
		return false, err
	}
	return live, nil
}

// RiverQueueDemand counts running and available deliveries, capped at the
// current capacity because a larger backlog cannot grow the consumer pool.
func (repository *SetupManagerRepository) RiverQueueDemand(ctx context.Context, queue string, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	var demand int
	if err := repository.db.NewRaw(`
		SELECT count(*) FROM (
			SELECT 1 FROM river_job
			WHERE queue = ? AND state IN ('running', 'available')
			LIMIT ?
		) AS demand`, queue, limit).Scan(ctx, &demand); err != nil {
		return 0, fmt.Errorf("count river queue demand: %w", err)
	}
	return demand, nil
}
