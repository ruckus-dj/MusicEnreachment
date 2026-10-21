package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FailPublicationSourceCleanupDelivery records an ambiguous cleanup failure.
// It retains all cleanup evidence and never retries or performs filesystem work.
func (repository *SetupManagerRepository) FailPublicationSourceCleanupDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, safeError string) (bool, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil || strings.TrimSpace(safeError) == "" {
		return false, fmt.Errorf("fail publication source cleanup delivery: valid guard and safe error are required")
	}
	safeError = strings.TrimSpace(safeError)
	failed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: lock source mutations: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: read output epoch: %w", err)
		}
		if epoch != guard.Epoch {
			return nil
		}
		var positionID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, guard.PositionID).Scan(ctx, &positionID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("fail publication source cleanup delivery: lock position: %w", err)
		}

		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("fail publication source cleanup delivery: lock operation: %w", err)
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: decode operation snapshot: %w", err)
		}
		if operation.Kind != "cleanup_publication_sources_v1" || operation.State != "running" || operation.Attempt != guard.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != guard.RiverJobID || summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch {
			return nil
		}

		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("fail publication source cleanup delivery: lock items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("fail publication source cleanup delivery: lock owners: %w", err)
		}
		if len(owners) != len(items) {
			return nil
		}
		startedIncomplete := make([]uuid.UUID, 0, len(items))
		for i, item := range items {
			owner := owners[i]
			if item.ID == uuid.Nil || item.OperationID != guard.OperationID || item.PositionID != guard.PositionID || item.Epoch != guard.Epoch || item.PublicationGeneration == uuid.Nil || item.State != "claimed" || len(item.SourceSnapshot) == 0 || item.BorrowerRevision == nil || *item.BorrowerRevision == uuid.Nil {
				return nil
			}
			var frozen publicationSourceCleanupSnapshot
			if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
				return fmt.Errorf("fail publication source cleanup delivery: decode frozen source identity: %w", err)
			}
			if frozen.Version != 1 || frozen.WorkID == uuid.Nil || frozen.SourceRootID == uuid.Nil || frozen.SourceLocationID == uuid.Nil || frozen.MediaVariantID == uuid.Nil || frozen.ConfiguredPath == "" || frozen.RelativePath == "" {
				return nil
			}
			if owner.ItemID != item.ID || owner.OperationID != guard.OperationID || owner.ApplicationAttempt != guard.ApplicationAttempt || owner.RiverJobID != guard.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID != frozen.SourceRootID || owner.SourceLocationID != frozen.SourceLocationID || owner.WorkID != frozen.WorkID {
				return nil
			}
			switch {
			case item.MutationClaimUUID == nil && item.MutationStartedAt == nil && item.MutationCompletedAt == nil:
				// Not started under this delivery; retain unchanged.
			case item.MutationClaimUUID != nil && *item.MutationClaimUUID == guard.ClaimUUID && item.MutationStartedAt != nil && !item.MutationStartedAt.IsZero() && item.MutationCompletedAt == nil:
				startedIncomplete = append(startedIncomplete, item.ID)
			case item.MutationClaimUUID != nil && *item.MutationClaimUUID == guard.ClaimUUID && item.MutationStartedAt != nil && !item.MutationStartedAt.IsZero() && item.MutationCompletedAt != nil && !item.MutationCompletedAt.Before(*item.MutationStartedAt):
				// Completed evidence remains untouched by a failure of another item.
			default:
				return nil
			}
		}
		if len(startedIncomplete) == 0 {
			return nil
		}

		var riverKind, riverState string
		var riverAttempt int
		var riverArgs []byte
		if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &riverArgs); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("fail publication source cleanup delivery: lock River job: %w", err)
		}
		var args publicationSourceCleanupJobArgs
		if err := decodeStrictJSON(riverArgs, &args); err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: decode River args: %w", err)
		}
		if riverKind != args.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
			return nil
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("fail publication source cleanup delivery: lock execution claims: %w", err)
		}
		if len(claims) != 1 || claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard) {
			return nil
		}

		for _, itemID := range startedIncomplete {
			result, err := tx.NewRaw(`UPDATE source_cleanup_item SET state='recovery_required',safe_error=? WHERE id=? AND operation_id=? AND state='claimed' AND mutation_claim_uuid=? AND mutation_started_at IS NOT NULL AND mutation_completed_at IS NULL`, safeError, itemID, guard.OperationID, guard.ClaimUUID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("fail publication source cleanup delivery: mark item recovery required: %w", err)
			}
			if err := requireRowsAffected(result, 1, "fail publication source cleanup item"); err != nil {
				return err
			}
		}
		claimResult, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='recovery_required' WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=? AND state='executing'`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: mark claim recovery required: %w", err)
		}
		if err := requireRowsAffected(claimResult, 1, "fail publication source cleanup claim"); err != nil {
			return err
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='cleanup_recovery_required',finished_at=now(),updated_at=now(),safe_error=?
			WHERE id=? AND kind='cleanup_publication_sources_v1' AND state='running' AND attempt=? AND river_job_id=?`, safeError, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail publication source cleanup delivery: fail operation: %w", err)
		}
		if err := requireRowsAffected(operationResult, 1, "fail publication source cleanup operation"); err != nil {
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
