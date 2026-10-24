package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	obsoleteOutputCleanupFailureStage = "cleanup"
	obsoleteOutputCleanupFailureSafe  = "obsolete publication output cleanup failed"
)

// ReadObsoletePublicationOutputCleanup returns the live authority for a cleanup
// operation. A missing row means the authority has already been settled.
func (repository *SetupManagerRepository) ReadObsoletePublicationOutputCleanup(ctx context.Context, operationID uuid.UUID) (*ObsoletePublicationOutputCleanup, error) {
	row := new(ObsoletePublicationOutputCleanup)
	err := repository.db.NewSelect().Model(row).Where("operation_id = ?", operationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read obsolete publication output cleanup: %w", err)
	}
	return row, nil
}

// ExecuteObsoletePublicationOutputCleanup holds the output-admission gate and
// all relevant durable authority locks while remove verifies and removes the
// exact output. The callback is never invoked for stale or terminal deliveries.
// Callback failures are committed as failed operation/authority state and then
// returned to the worker; context cancellation rolls the transaction back so
// River can redeliver the still-live authority.
func (repository *SetupManagerRepository) ExecuteObsoletePublicationOutputCleanup(
	ctx context.Context,
	operationID uuid.UUID,
	applicationAttempt int,
	riverJobID int64,
	remove func(context.Context, ObsoletePublicationOutputCleanup) error,
) (bool, error) {
	if remove == nil {
		return false, errors.New("execute obsolete publication output cleanup: remove callback is required")
	}
	var acquired bool
	var callbackErr error
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: lock output admission gate: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: lock operation: %w", err)
		}
		if operation.Kind != ObsoletePublicationOutputCleanupOperationKind || operation.Attempt != applicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID || (operation.State != "queued" && operation.State != "running") {
			return nil
		}
		row := new(ObsoletePublicationOutputCleanup)
		if err := tx.NewSelect().Model(row).Where("operation_id = ?", operationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("execute obsolete publication output cleanup: lock authority: %w", err)
		}
		var activeEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &activeEpoch); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: lock namespace epoch: %w", err)
		}
		claim := new(PublicationNamespaceClaim)
		if err := tx.NewSelect().Model(claim).Where("id = ? AND epoch = ? AND relative_path = ? AND node_kind = 'file' AND owner_position_id = ?", row.NamespaceClaimID, row.Epoch, row.RelativePath, row.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("execute obsolete publication output cleanup: lock namespace claim: %w", err)
		}
		reservation := new(PublicationNamespaceReservation)
		if err := tx.NewSelect().Model(reservation).Where("namespace_claim_id = ? AND position_id = ? AND generation = ?", row.NamespaceClaimID, row.PositionID, row.Generation).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("execute obsolete publication output cleanup: lock namespace reservation: %w", err)
		}
		if activeEpoch != row.Epoch {
			return nil
		}
		var protected bool
		if err := tx.NewRaw(`
			SELECT EXISTS (
				SELECT 1 FROM publication_namespace_reservation r
				JOIN track_publication p ON p.position_id = r.position_id AND p.epoch = ? AND p.generation = r.generation
				WHERE r.namespace_claim_id = ?
				UNION ALL
				SELECT 1 FROM publication_namespace_reservation r
				JOIN track_publication_attempt a ON a.position_id = r.position_id AND a.epoch = ? AND a.candidate_generation = r.generation
				WHERE r.namespace_claim_id = ?
				UNION ALL
				SELECT 1 FROM publication_namespace_reservation r
				WHERE r.namespace_claim_id = ? AND NOT (r.position_id = ? AND r.generation = ?)
			)`, row.Epoch, row.NamespaceClaimID, row.Epoch, row.NamespaceClaimID, row.NamespaceClaimID, row.PositionID, row.Generation).Scan(ctx, &protected); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: check namespace consumers: %w", err)
		}
		if protected {
			return nil
		}

		now := time.Now().UTC()
		operation.State, operation.Stage, operation.StartedAt, operation.FinishedAt = "running", "cleanup", &now, nil
		operation.SafeError, operation.UpdatedAt = nil, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "started_at", "finished_at", "safe_error", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: mark running: %w", err)
		}
		row.State, row.SafeError = "running", nil
		if _, err := tx.NewUpdate().Model(row).Column("state", "safe_error").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: mark authority running: %w", err)
		}
		acquired = true
		callbackErr = remove(ctx, *row)
		if callbackErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			safeError := obsoleteOutputCleanupFailureSafe
			row.State, row.SafeError = "failed", &safeError
			if _, err := tx.NewUpdate().Model(row).Column("state", "safe_error").WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("execute obsolete publication output cleanup: record failure authority: %w", err)
			}
			operation.State, operation.Stage, operation.FinishedAt, operation.SafeError, operation.UpdatedAt = "failed", obsoleteOutputCleanupFailureStage, &now, &safeError, now
			if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "finished_at", "safe_error", "updated_at").WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("execute obsolete publication output cleanup: record failure: %w", err)
			}
			return nil
		}
		if _, err := tx.NewDelete().Model(row).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: retire authority: %w", err)
		}
		if _, err := tx.NewDelete().Model(reservation).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: release reservation: %w", err)
		}
		var reservationsRemain bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE namespace_claim_id = ?)`, row.NamespaceClaimID).Scan(ctx, &reservationsRemain); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: check remaining reservations: %w", err)
		}
		if !reservationsRemain && claim.NodeKind != "directory" {
			if _, err := tx.NewDelete().Model(claim).WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("execute obsolete publication output cleanup: release namespace claim: %w", err)
			}
		}
		operation.State, operation.Stage, operation.FinishedAt, operation.SafeError, operation.UpdatedAt = "succeeded", "finished", &now, nil, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "finished_at", "safe_error", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("execute obsolete publication output cleanup: finish operation: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if acquired && callbackErr != nil {
		return true, fmt.Errorf("execute obsolete publication output cleanup: %w", callbackErr)
	}
	return acquired, nil
}
