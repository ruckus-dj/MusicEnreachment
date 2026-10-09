package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrToolsExecutionClaimHeld = errors.New("tools filesystem recovery is still required")

// ClaimToolsExecutionDelivery fences tools filesystem work by the durable
// operation attempt and River job before a worker inspects or mutates disk.
func (repository *SetupManagerRepository) ClaimToolsExecutionDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, borrower string) (bool, error) {
	claimed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Admission has to precede the operation row lock, matching the lock order
		// used by output reset and tools-root coordination.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("claim tools operation delivery: lock output admission gate: %w", err)
		}
		var operation struct {
			Kind    string `bun:"kind"`
			State   string `bun:"state"`
			Attempt int    `bun:"attempt"`
			JobID   *int64 `bun:"river_job_id"`
		}
		if err := tx.NewSelect().Table("operation").Column("kind", "state", "attempt", "river_job_id").
			Where("id = ?", operationID).For("UPDATE").Scan(ctx, &operation); err != nil {
			return fmt.Errorf("lock tools operation delivery: %w", err)
		}
		if operation.Kind != borrower ||
			operation.Attempt != attempt || operation.JobID == nil || *operation.JobID != riverJobID {
			return nil
		}
		if operation.State != "queued" && operation.State != "running" && operation.State != "succeeded" && operation.State != "failed" {
			return nil
		}
		result, err := tx.NewRaw(`INSERT INTO tools_execution_claim(operation_id, attempt, river_job_id, borrower, borrowed)
			VALUES (?, ?, ?, ?, true)
			ON CONFLICT (operation_id) DO UPDATE SET
				claimed_at = now(), borrowed = true
			WHERE tools_execution_claim.attempt = EXCLUDED.attempt AND
				 tools_execution_claim.river_job_id = EXCLUDED.river_job_id AND
				 tools_execution_claim.borrower = EXCLUDED.borrower AND
				 NOT tools_execution_claim.borrowed`,
			operationID, attempt, riverJobID, borrower).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim tools operation delivery: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrToolsExecutionClaimHeld
		}
		if operation.State == "queued" {
			if _, err := tx.NewRaw(`UPDATE operation SET state='running',
				started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=? AND attempt=? AND river_job_id=? AND state='queued'`,
				operationID, attempt, riverJobID).Exec(ctx); err != nil {
				return fmt.Errorf("start claimed tools operation: %w", err)
			}
		}
		claimed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

// AbandonToolsExecutionDelivery keeps unresolved filesystem ownership durable,
// but makes it available to a later delivery after the current callback ended.
func (repository *SetupManagerRepository) AbandonToolsExecutionDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, borrower string) error {
	_, err := repository.db.NewUpdate().Table("tools_execution_claim").
		Set("borrowed = false").
		Where("operation_id = ? AND attempt = ? AND river_job_id = ? AND borrower = ?", operationID, attempt, riverJobID, borrower).Exec(ctx)
	if err != nil {
		return fmt.Errorf("abandon tools operation delivery: %w", err)
	}
	return nil
}

// ReleaseToolsExecutionDelivery removes only the claim owned by this delivery.
func (repository *SetupManagerRepository) ReleaseToolsExecutionDelivery(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, borrower string) error {
	_, err := repository.db.NewDelete().Table("tools_execution_claim").
		Where("operation_id = ? AND attempt = ? AND river_job_id = ? AND borrower = ?", operationID, attempt, riverJobID, borrower).Exec(ctx)
	if err != nil {
		return fmt.Errorf("release tools operation delivery: %w", err)
	}
	return nil
}
