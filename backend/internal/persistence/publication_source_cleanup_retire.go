package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type publicationSourceCleanupRetirementMode uint8

const (
	retirePublicationSourceCleanupDisabled publicationSourceCleanupRetirementMode = iota + 1
	retirePublicationSourceCleanupUnstartedFailure
)

// FailUnstartedPublicationSourceCleanupDelivery retires an exact claimed delivery
// after removal could not safely start. It does not authorize unlinking.
func (repository *SetupManagerRepository) FailUnstartedPublicationSourceCleanupDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, reason string) (bool, error) {
	return repository.retirePublicationSourceCleanupDelivery(ctx, guard, reason, retirePublicationSourceCleanupUnstartedFailure)
}

func (repository *SetupManagerRepository) retirePublicationSourceCleanupDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, reason string, mode publicationSourceCleanupRetirementMode) (bool, error) {
	if mode != retirePublicationSourceCleanupDisabled && mode != retirePublicationSourceCleanupUnstartedFailure {
		return false, fmt.Errorf("retire publication source cleanup: invalid retirement mode")
	}
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || strings.TrimSpace(guard.DeliveryToken) == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return false, fmt.Errorf("retire publication source cleanup: valid delivery guard and reason are required")
	}
	cancelled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retire publication source cleanup: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("retire publication source cleanup: lock source mutations: %w", err)
		}
		if mode == retirePublicationSourceCleanupDisabled {
			var enabledValue string
			err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &enabledValue)
			enabled := false
			switch err {
			case nil:
				var parseErr error
				enabled, parseErr = strconv.ParseBool(enabledValue)
				if parseErr != nil {
					return fmt.Errorf("retire publication source cleanup: parse publication_remove_sources: %w", parseErr)
				}
			case sql.ErrNoRows:
			default:
				return fmt.Errorf("retire publication source cleanup: read publication_remove_sources: %w", err)
			}
			if enabled {
				return nil
			}
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("retire publication source cleanup: read output epoch: %w", err)
		}
		if epoch != guard.Epoch {
			return nil
		}
		var positionID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, guard.PositionID).Scan(ctx, &positionID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("retire publication source cleanup: lock position: %w", err)
		}
		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("retire publication source cleanup: lock operation: %w", err)
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("retire publication source cleanup: decode operation snapshot: %w", err)
		}
		if operation.Kind != "cleanup_publication_sources_v1" || (operation.State != "queued" && operation.State != "running") || operation.Attempt != guard.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != guard.RiverJobID || summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch {
			return nil
		}
		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("retire publication source cleanup: lock items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("retire publication source cleanup: lock owners: %w", err)
		}
		if len(owners) != len(items) {
			return nil
		}
		for i, item := range items {
			owner := owners[i]
			if item.ID == uuid.Nil || item.OperationID != guard.OperationID || item.PositionID != guard.PositionID || item.Epoch != guard.Epoch || item.PublicationGeneration == uuid.Nil || item.BorrowerRevision == nil || *item.BorrowerRevision == uuid.Nil || item.MutationClaimUUID != nil || item.MutationStartedAt != nil || item.MutationCompletedAt != nil {
				return nil
			}
			wantState := "queued"
			if operation.State == "running" {
				wantState = "claimed"
			}
			if item.State != wantState || len(item.SourceSnapshot) == 0 {
				return nil
			}
			var frozen publicationSourceCleanupSnapshot
			if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
				return fmt.Errorf("retire publication source cleanup: decode frozen source identity: %w", err)
			}
			if frozen.Version != 1 || frozen.WorkID == uuid.Nil || frozen.SourceRootID == uuid.Nil || frozen.SourceLocationID == uuid.Nil || frozen.MediaVariantID == uuid.Nil || frozen.ConfiguredPath == "" || frozen.RelativePath == "" || owner.ItemID != item.ID || owner.OperationID != guard.OperationID || owner.ApplicationAttempt != guard.ApplicationAttempt || owner.RiverJobID != guard.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID != frozen.SourceRootID || owner.SourceLocationID != frozen.SourceLocationID || owner.WorkID != frozen.WorkID {
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
			return fmt.Errorf("retire publication source cleanup: lock River job: %w", err)
		}
		var args publicationSourceCleanupJobArgs
		if err := decodeStrictJSON(riverArgs, &args); err != nil {
			return fmt.Errorf("retire publication source cleanup: decode River args: %w", err)
		}
		if riverKind != args.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
			return nil
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("retire publication source cleanup: lock execution claims: %w", err)
		}
		if mode == retirePublicationSourceCleanupDisabled {
			if len(claims) > 1 || (len(claims) == 1 && (claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard))) || (operation.State == "running" && len(claims) != 1) {
				return nil
			}
		} else if len(claims) != 1 || claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard) {
			return nil
		}
		ownerDelete, err := tx.NewRaw(`DELETE FROM source_cleanup_owner WHERE operation_id=?`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("retire publication source cleanup: delete owners: %w", err)
		}
		if err := requireRowsAffected(ownerDelete, int64(len(owners)), "cancel publication source cleanup owners"); err != nil {
			return err
		}
		itemDelete, err := tx.NewRaw(`DELETE FROM source_cleanup_item WHERE operation_id=?`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("retire publication source cleanup: delete items: %w", err)
		}
		if err := requireRowsAffected(itemDelete, int64(len(items)), "cancel publication source cleanup items"); err != nil {
			return err
		}
		if len(claims) == 1 {
			claimDelete, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=? AND state='executing'`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch).Exec(ctx)
			if err != nil {
				return fmt.Errorf("retire publication source cleanup: delete execution claim: %w", err)
			}
			if err := requireRowsAffected(claimDelete, 1, "cancel publication source cleanup execution claim"); err != nil {
				return err
			}
		}
		operationUpdate, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='finished',finished_at=now(),updated_at=now(),safe_error=?
			WHERE id=? AND kind='cleanup_publication_sources_v1' AND state=? AND attempt=? AND river_job_id=?`, strings.TrimSpace(reason), guard.OperationID, operation.State, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("retire publication source cleanup: fail operation: %w", err)
		}
		if err := requireRowsAffected(operationUpdate, 1, "cancel publication source cleanup operation"); err != nil {
			return err
		}
		cancelled = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return cancelled, nil
}
