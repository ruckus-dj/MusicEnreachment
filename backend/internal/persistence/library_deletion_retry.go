package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FailLibraryDeletionDelivery records an owner-returned failure without
// changing frozen scope, ownership witnesses, or residual publication progress.
func (repository *SetupManagerRepository) FailLibraryDeletionDelivery(
	ctx context.Context,
	guard LibraryDeletionDeliveryGuard,
	safeError string,
) (bool, error) {
	if err := validateLibraryDeletionDeliveryGuard(guard); err != nil {
		return false, err
	}
	safeError = strings.TrimSpace(safeError)
	if safeError == "" {
		return false, errors.New("fail library deletion delivery: safe error is required")
	}
	failed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("fail library deletion delivery: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("fail library deletion delivery: lock source mutations: %w", err)
		}
		snapshot, operation, _, _, live, err := lockLibraryDeletionScope(ctx, tx, guard, "fail library deletion delivery")
		if err != nil || !live {
			return err
		}
		if operation.State != "running" {
			return nil
		}
		if err := lockAndValidateLibraryDeletionRiver(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := requireLibraryDeletionClaim(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		// The failure is recorded only after the invocation has stopped. Retain
		// the frozen scope and residual IDs so explicit retry resumes safely.
		if err := validateLibraryDeletionSnapshot(snapshot); err != nil {
			return fmt.Errorf("fail library deletion delivery: validate retained snapshot: %w", err)
		}
		// Do not replace or normalize this snapshot on failure. It may describe
		// partially settled work and is the sole authority for an explicit retry.
		claimResult, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='recovery_required'
			WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND state='executing'`,
			guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt,
			guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail library deletion delivery: mark claim recovery required: %w", err)
		}
		if err := requireRowsAffected(claimResult, 1, "mark library deletion claim recovery required"); err != nil {
			return err
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='deletion_recovery_required',safe_error=?,finished_at=now(),updated_at=now()
			WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`, safeError,
			guard.OperationID, LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail library deletion delivery: fail operation: %w", err)
		}
		if err := requireRowsAffected(operationResult, 1, "fail library deletion operation"); err != nil {
			return err
		}
		failed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return failed, nil
}

// RetryLibraryDeletionOperationAndEnqueue explicitly resumes a failed deletion
// using the same operation and immutable snapshot. A new application attempt
// and its ID-only River job are inserted atomically.
func (repository *SetupManagerRepository) RetryLibraryDeletionOperationAndEnqueue(
	ctx context.Context,
	id uuid.UUID,
	client RiverInserter,
) (*Operation, error) {
	if id == uuid.Nil || client == nil {
		return nil, errors.New("retry library deletion: operation and River client are required")
	}
	captured, err := repository.GetOperation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("retry library deletion: read operation: %w", err)
	}
	var capturedSnapshot LibraryDeletionOperationSnapshot
	if captured.Kind != LibraryDeletionOperationKind || captured.State != "failed" || captured.Stage != "deletion_recovery_required" ||
		decodeStrictJSON(captured.InputSnapshot, &capturedSnapshot) != nil || validateLibraryDeletionSnapshot(capturedSnapshot) != nil {
		return nil, errors.New("retry library deletion: operation has no retryable retained deletion state")
	}

	var retried *Operation
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retry library deletion: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("retry library deletion: lock source mutations: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("retry library deletion: lock output epoch: %w", err)
		}
		var releaseID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, capturedSnapshot.Scope.ReleaseID).Scan(ctx, &releaseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryReleaseNotFound
			}
			return fmt.Errorf("retry library deletion: lock release: %w", err)
		}
		var positions []uuid.UUID
		positionQuery := `SELECT id FROM release_track WHERE release_id=? ORDER BY id FOR UPDATE`
		positionArgs := []any{releaseID}
		if capturedSnapshot.Scope.Kind == "publication" {
			positionQuery = `SELECT id FROM release_track WHERE release_id=? AND id=? ORDER BY id FOR UPDATE`
			positionArgs = append(positionArgs, capturedSnapshot.Scope.PublicationIDs[0])
		}
		if err := tx.NewRaw(positionQuery, positionArgs...).Scan(ctx, &positions); err != nil {
			return fmt.Errorf("retry library deletion: lock release positions: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("retry library deletion: lock operation: %w", err)
		}
		var snapshot LibraryDeletionOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &snapshot); err != nil {
			return fmt.Errorf("retry library deletion: decode retained snapshot: %w", err)
		}
		if operation.Kind != LibraryDeletionOperationKind || operation.State != "failed" || operation.Stage != "deletion_recovery_required" ||
			operation.Attempt != captured.Attempt || !sameScanJobID(operation.RiverJobID, captured.RiverJobID) ||
			!sameLibraryDeletionSnapshot(snapshot, capturedSnapshot) {
			return errors.New("retry library deletion: retained operation changed")
		}
		if operation.RiverJobID == nil || operation.Attempt <= 0 {
			return errors.New("retry library deletion: prior delivery identity is unavailable")
		}
		if err := validateLibraryDeletionFrozenScope(ctx, tx, snapshot, epoch, positions); err != nil {
			return fmt.Errorf("retry library deletion: frozen scope is no longer eligible: %w", err)
		}
		var claims []PublicationExecutionClaim
		claimErr := tx.NewSelect().Model(&claims).Where("operation_id=?", id).Order("application_attempt").For("UPDATE").Scan(ctx)
		if claimErr != nil && !errors.Is(claimErr, sql.ErrNoRows) {
			return fmt.Errorf("retry library deletion: lock prior delivery claim: %w", claimErr)
		}
		if len(claims) > 1 || len(claims) == 1 && (claims[0].ApplicationAttempt != operation.Attempt || claims[0].RiverJobID != *operation.RiverJobID || claims[0].State != "recovery_required") {
			return errors.New("retry library deletion: prior delivery has no retained recovery claim")
		}
		if len(claims) == 1 {
			if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND state='recovery_required'`,
				id, operation.Attempt, *operation.RiverJobID).Exec(ctx); err != nil {
				return fmt.Errorf("retry library deletion: retire prior execution claim: %w", err)
			}
		}
		inserted, err := client.InsertTx(ctx, tx.Tx, LibraryDeletionOperationArgs{OperationID: id}, nil)
		if err != nil {
			return fmt.Errorf("retry library deletion: insert River job: %w", err)
		}
		if inserted == nil || inserted.Job == nil || inserted.Job.ID <= 0 {
			return errors.New("retry library deletion: River inserter returned no job identity")
		}
		operation.State = "queued"
		operation.Stage = "retry:deletion_recovery_required"
		operation.SafeError = nil
		operation.StartedAt = nil
		operation.FinishedAt = nil
		operation.Attempt++
		operation.RiverJobID = &inserted.Job.ID
		operation.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(operation).
			Column("state", "stage", "safe_error", "started_at", "finished_at", "attempt", "river_job_id", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry library deletion: update operation: %w", err)
		}
		retried = operation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return retried, nil
}

func sameLibraryDeletionSnapshot(left, right LibraryDeletionOperationSnapshot) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
