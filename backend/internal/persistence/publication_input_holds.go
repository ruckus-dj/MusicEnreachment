package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
)

// ReleasePublicationInputHolds releases only the durable source/tool protections
// for a committed delivery whose caller has already closed its local execution
// scope. The persisted quiescent claim is a trusted database fence, not proof
// that an independent process has stopped using these resources.
func (repository *SetupManagerRepository) ReleasePublicationInputHolds(ctx context.Context, guard PublicationDeliveryGuard) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	released := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("release publication input holds: acquire output admission: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("release publication input holds: lock source mutations: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("release publication input holds: lock tools root: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg"); err != nil {
			return fmt.Errorf("release publication input holds: lock ffmpeg package: %w", err)
		}
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "release publication input holds")
		if err != nil || !live {
			return err
		}
		var frozenIsNull bool
		if err := tx.NewRaw(`SELECT frozen_inputs IS NULL FROM track_publication_attempt WHERE operation_id=?`, guard.OperationID).Scan(ctx, &frozenIsNull); err != nil {
			return fmt.Errorf("release publication input holds: inspect frozen inputs: %w", err)
		}
		if operation.State != "running" || operation.Stage != "committed" || attempt.Phase != "committed" || attempt.IntentKind != "audio" || attempt.ExpectedGeneration != nil || !frozenIsNull || claim.State != "quiescent" {
			return nil
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("release publication input holds: read current publication: %w", err)
		}
		if !committedWitnessMatches(attempt.MutationJournal, guard, attempt, current) {
			return nil
		}

		var sourceHoldCount int
		if err := tx.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, guard.OperationID).Scan(ctx, &sourceHoldCount); err != nil {
			return fmt.Errorf("release publication input holds: inspect source hold: %w", err)
		}
		if sourceHoldCount > 0 {
			var hold PublicationSourceHold
			if err := tx.NewSelect().Model(&hold).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
				return fmt.Errorf("release publication input holds: lock source hold: %w", err)
			}
			if hold.ApplicationAttempt != guard.ApplicationAttempt || hold.RiverJobID != guard.RiverJobID || hold.Epoch != guard.Epoch {
				return nil
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND epoch=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.Epoch).Exec(ctx); err != nil {
			return fmt.Errorf("release publication input holds: delete source hold: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("release publication input holds: delete tool holds: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=false WHERE id=? AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx); err != nil {
			return fmt.Errorf("release publication input holds: clear tools-read flag: %w", err)
		}
		released = true
		return nil
	})
	if err != nil {
		// RunInTx errors always override the callback result: commit outcome may be
		// uncertain even when the callback set released=true.
		return false, err
	}
	return released, nil
}
