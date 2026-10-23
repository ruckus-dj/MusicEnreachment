package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const failedPublicationDeliveryError = "Publication delivery was cancelled or discarded before completion. Retry the publication to continue."

// TerminalPublicationCandidate is a bounded maintenance candidate for a
// publication whose currently registered River job is terminal or missing.
// The caller must pass the captured identity back to FailTerminalPublication;
// that method rechecks it under lock before changing state.
type TerminalPublicationCandidate struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	RiverJobID         int64
	RiverState         string
}

// ListTerminalPublicationCandidates returns current active publication
// deliveries whose registered River job is cancelled, discarded, or missing.
func (repository *SetupManagerRepository) ListTerminalPublicationCandidates(ctx context.Context, limit int) ([]TerminalPublicationCandidate, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("terminal publication candidate limit must be between 1 and 500")
	}
	var candidates []TerminalPublicationCandidate
	err := repository.db.NewRaw(`
		SELECT o.id AS operation_id, o.attempt AS application_attempt,
		       o.river_job_id, COALESCE(j.state::text, 'missing') AS river_state
		FROM operation o
		LEFT JOIN river_job j ON j.id=o.river_job_id
		WHERE o.kind='publish_track_v1' AND o.state IN ('queued','running')
		  AND o.river_job_id IS NOT NULL
		  AND (j.id IS NULL OR j.state IN ('cancelled','discarded'))
		ORDER BY o.updated_at, o.id
		LIMIT ?`, limit).Scan(ctx, &candidates)
	if err != nil {
		return nil, fmt.Errorf("list terminal publication candidates: %w", err)
	}
	return candidates, nil
}

// FailTerminalPublication fails only the exact still-active application
// delivery captured by maintenance. It does not infer worker failure from a
// worker return, context cancellation, or process shutdown.
func (repository *SetupManagerRepository) FailTerminalPublication(ctx context.Context, candidate TerminalPublicationCandidate) (bool, error) {
	if candidate.OperationID == uuid.Nil || candidate.ApplicationAttempt < 1 || candidate.RiverJobID < 1 ||
		(candidate.RiverState != "cancelled" && candidate.RiverState != "discarded" && candidate.RiverState != "missing") {
		return false, errors.New("invalid terminal publication candidate")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("fail terminal publication: lock output admission gate: %w", err)
		}
		var positionID uuid.UUID
		if err := tx.NewRaw(`SELECT position_id FROM track_publication_attempt WHERE operation_id=?`, candidate.OperationID).Scan(ctx, &positionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail terminal publication: read attempt position: %w", err)
		}
		var lockedPosition uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &lockedPosition); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail terminal publication: lock position: %w", err)
		}
		attempt := new(TrackPublicationAttempt)
		if err := tx.NewSelect().Model(attempt).Where("operation_id=?", candidate.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail terminal publication: lock attempt: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewSelect().Model(operation).Where("id=?", candidate.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail terminal publication: lock operation: %w", err)
		}
		if operation.Kind != "publish_track_v1" || (operation.State != "queued" && operation.State != "running") ||
			operation.Attempt != candidate.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != candidate.RiverJobID {
			return nil
		}
		var riverState string
		err := tx.NewRaw(`SELECT state FROM river_job WHERE id=? FOR UPDATE`, candidate.RiverJobID).Scan(ctx, &riverState)
		if errors.Is(err, sql.ErrNoRows) {
			riverState = "missing"
		} else if err != nil {
			return fmt.Errorf("fail terminal publication: lock River job: %w", err)
		}
		if riverState != candidate.RiverState || (riverState != "cancelled" && riverState != "discarded" && riverState != "missing") {
			return nil
		}
		result, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='failed',safe_error=?,finished_at=now(),updated_at=now()
			WHERE id=? AND kind='publish_track_v1' AND state IN ('queued','running') AND attempt=? AND river_job_id=?`,
			failedPublicationDeliveryError, candidate.OperationID, candidate.ApplicationAttempt, candidate.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail terminal publication: update operation: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("fail terminal publication: inspect operation update: %w", err)
		}
		changed = rows == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}
