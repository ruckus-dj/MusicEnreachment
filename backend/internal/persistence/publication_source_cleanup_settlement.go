package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SettlePublicationSourceCleanup releases a completed cleanup delivery. It
// acknowledges the durable unlink frontier only; it does not re-authorize or
// perform filesystem mutation, nor does it change source inventory or links.
func (repository *SetupManagerRepository) SettlePublicationSourceCleanup(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard) (bool, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return false, fmt.Errorf("settle publication source cleanup: invalid delivery guard")
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("settle publication source cleanup: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("settle publication source cleanup: lock source mutations: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("settle publication source cleanup: read output epoch: %w", err)
		}
		if epoch != guard.Epoch {
			return nil
		}
		var positionID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, guard.PositionID).Scan(ctx, &positionID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("settle publication source cleanup: lock position: %w", err)
		}

		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("settle publication source cleanup: lock operation: %w", err)
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("settle publication source cleanup: decode operation snapshot: %w", err)
		}
		if operation.Kind != "cleanup_publication_sources_v1" || operation.State != "running" || operation.Attempt != guard.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != guard.RiverJobID || summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch {
			return nil
		}

		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("settle publication source cleanup: lock items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("settle publication source cleanup: lock owners: %w", err)
		}
		if len(owners) != len(items) {
			return nil
		}
		for i, item := range items {
			owner := owners[i]
			if item.ID == uuid.Nil || item.OperationID != guard.OperationID || item.PositionID != guard.PositionID || item.Epoch != guard.Epoch || item.PublicationGeneration == uuid.Nil || item.State != "claimed" || len(item.SourceSnapshot) == 0 || item.BorrowerRevision == nil || *item.BorrowerRevision == uuid.Nil || item.MutationClaimUUID == nil || *item.MutationClaimUUID != guard.ClaimUUID || item.MutationStartedAt == nil || item.MutationStartedAt.IsZero() || item.MutationCompletedAt == nil || item.MutationCompletedAt.Before(*item.MutationStartedAt) {
				return nil
			}
			var frozen publicationSourceCleanupSnapshot
			if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
				return fmt.Errorf("settle publication source cleanup: decode frozen source identity: %w", err)
			}
			if frozen.Version != 1 || frozen.WorkID == uuid.Nil || frozen.SourceRootID == uuid.Nil || frozen.SourceLocationID == uuid.Nil || frozen.MediaVariantID == uuid.Nil || frozen.ConfiguredPath == "" || frozen.RelativePath == "" {
				return nil
			}
			if owner.ItemID != item.ID || owner.OperationID != guard.OperationID || owner.ApplicationAttempt != guard.ApplicationAttempt || owner.RiverJobID != guard.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID != frozen.SourceRootID || owner.SourceLocationID != frozen.SourceLocationID || owner.WorkID != frozen.WorkID {
				return nil
			}
		}

		var riverKind, riverState string
		var riverAttempt int
		var riverArgs []byte
		if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &riverArgs); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("settle publication source cleanup: lock River job: %w", err)
		}
		var args publicationSourceCleanupJobArgs
		if err := decodeStrictJSON(riverArgs, &args); err != nil {
			return fmt.Errorf("settle publication source cleanup: decode River args: %w", err)
		}
		if riverKind != args.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
			return nil
		}

		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("settle publication source cleanup: lock execution claims: %w", err)
		}
		if len(claims) != 1 || claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard) {
			return nil
		}

		ownerDelete, err := tx.NewRaw(`DELETE FROM source_cleanup_owner WHERE operation_id=?`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle publication source cleanup: delete owners: %w", err)
		}
		if err := requireRowsAffected(ownerDelete, int64(len(owners)), "settle publication source cleanup owners"); err != nil {
			return err
		}
		itemDelete, err := tx.NewRaw(`DELETE FROM source_cleanup_item WHERE operation_id=? AND state='claimed'`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle publication source cleanup: delete items: %w", err)
		}
		if err := requireRowsAffected(itemDelete, int64(len(items)), "settle publication source cleanup items"); err != nil {
			return err
		}
		claimDelete, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=? AND state='executing'`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle publication source cleanup: delete execution claim: %w", err)
		}
		if err := requireRowsAffected(claimDelete, 1, "settle publication source cleanup execution claim"); err != nil {
			return err
		}
		operationUpdate, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='finished',finished_at=now(),updated_at=now(),safe_error=NULL
			WHERE id=? AND kind='cleanup_publication_sources_v1' AND state='running' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle publication source cleanup: succeed operation: %w", err)
		}
		if err := requireRowsAffected(operationUpdate, 1, "settle publication source cleanup operation"); err != nil {
			return err
		}
		settled = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return settled, nil
}
