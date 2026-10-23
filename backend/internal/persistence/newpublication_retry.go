package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// RetryPublicationOperationAndEnqueue retries one failed first-audio or rewrite
// publication under the same operation and frozen attempt. The operation, its
// source hold identity transfer, and the new River job are committed atomically.
func (repository *SetupManagerRepository) RetryPublicationOperationAndEnqueue(ctx context.Context, id uuid.UUID, client RiverInserter) (*Operation, error) {
	if client == nil {
		return nil, errors.New("retry publication: River client is required")
	}
	var retried *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retry publication: lock output admission gate: %w", err)
		}
		captured := new(Operation)
		if err := tx.NewSelect().Model(captured).Where("id=?", id).Scan(ctx); err != nil {
			return fmt.Errorf("retry publication: read operation: %w", err)
		}
		if captured.Kind != "publish_track_v1" || captured.State != "failed" {
			return errors.New("retry publication: only a failed publication can be retried")
		}
		var positionID, epoch uuid.UUID
		if err := tx.NewRaw(`SELECT position_id,epoch FROM track_publication_attempt WHERE operation_id=?`, id).Scan(ctx, &positionID, &epoch); err != nil {
			return fmt.Errorf("retry publication: read frozen attempt identity: %w", err)
		}
		var currentEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &currentEpoch); err != nil {
			return fmt.Errorf("retry publication: lock namespace epoch: %w", err)
		}
		if currentEpoch != epoch {
			return errors.New("retry publication: output namespace changed since admission")
		}
		var lockedPosition uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &lockedPosition); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("retry publication: publication position no longer exists")
			}
			return fmt.Errorf("retry publication: lock position: %w", err)
		}
		attempt := new(TrackPublicationAttempt)
		if err := tx.NewSelect().Model(attempt).Where("operation_id=?", id).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("retry publication: lock attempt: %w", err)
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("retry publication: lock operation: %w", err)
		}
		if operation.Kind != "publish_track_v1" || operation.State != "failed" ||
			operation.Attempt != captured.Attempt || !sameScanJobID(operation.RiverJobID, captured.RiverJobID) ||
			attempt.PositionID != positionID || attempt.Epoch != epoch ||
			(attempt.IntentKind != "audio" && attempt.IntentKind != "rewrite") ||
			attempt.Phase == "committed" || attempt.Phase == "cleaning" || attempt.Phase == "cleanup_pending" ||
			len(attempt.FrozenInputs) == 0 || len(attempt.NamespaceManifest) == 0 {
			return errors.New("retry publication: failed attempt is not a retryable simple publication")
		}
		var currentGeneration *uuid.UUID
		readErr := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, positionID).Scan(ctx, &currentGeneration)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return fmt.Errorf("retry publication: read current publication generation: %w", readErr)
		}
		if !sameOptionalUUID(currentGeneration, attempt.ExpectedGeneration) {
			return errors.New("retry publication: current publication generation changed")
		}

		var held *PublicationSourceHold
		if operation.RiverJobID != nil {
			var riverState string
			err = tx.NewRaw(`SELECT state FROM river_job WHERE id=? FOR UPDATE`, *operation.RiverJobID).Scan(ctx, &riverState)
			if errors.Is(err, sql.ErrNoRows) {
				riverState = "missing"
			} else if err != nil {
				return fmt.Errorf("retry publication: lock previous River job: %w", err)
			}
			if riverState != "missing" && riverState != "completed" && riverState != "cancelled" && riverState != "discarded" {
				return errors.New("retry publication: previous River job is not terminal")
			}
			var hold PublicationSourceHold
			err = tx.NewSelect().Model(&hold).Where("operation_id=?", id).For("UPDATE").Scan(ctx)
			if err == nil {
				if hold.ApplicationAttempt != operation.Attempt || hold.RiverJobID != *operation.RiverJobID || hold.Epoch != attempt.Epoch {
					return errors.New("retry publication: source hold does not match the failed delivery")
				}
				held = &hold
			} else if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("retry publication: lock source hold: %w", err)
			}
		}
		if operation.RiverJobID == nil {
			return errors.New("retry publication: failed operation has no registered River job")
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=?`, id).Exec(ctx); err != nil {
			return fmt.Errorf("retry publication: clear obsolete execution claim: %w", err)
		}
		if held != nil {
			if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id=?`, id).Exec(ctx); err != nil {
				return fmt.Errorf("retry publication: release previous source-hold identity: %w", err)
			}
		}
		result, err := client.InsertTx(ctx, tx.Tx, publication.TrackArgs{OperationID: id}, &river.InsertOpts{Queue: "publication"})
		if err != nil {
			return fmt.Errorf("retry publication: insert River job: %w", err)
		}
		operation.State = "queued"
		operation.Stage = "queued"
		operation.SafeError = nil
		operation.StartedAt = nil
		operation.FinishedAt = nil
		operation.BytesCompleted = 0
		operation.BytesTotal = nil
		operation.RiverJobID = &result.Job.ID
		operation.Attempt++
		if err := tx.NewRaw(`SELECT clock_timestamp()`).Scan(ctx, &operation.UpdatedAt); err != nil {
			return fmt.Errorf("retry publication: read database clock: %w", err)
		}
		if _, err := tx.NewUpdate().Model(operation).
			Column("state", "stage", "safe_error", "started_at", "finished_at", "bytes_completed", "bytes_total", "river_job_id", "attempt", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry publication: update operation: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='queued' WHERE operation_id=?`, id).Exec(ctx); err != nil {
			return fmt.Errorf("retry publication: queue frozen attempt: %w", err)
		}
		if held != nil {
			held.ApplicationAttempt = operation.Attempt
			held.RiverJobID = result.Job.ID
			if _, err := tx.NewInsert().Model(held).Exec(ctx); err != nil {
				return fmt.Errorf("retry publication: transfer source hold: %w", err)
			}
		}
		retried = operation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return retried, nil
}
