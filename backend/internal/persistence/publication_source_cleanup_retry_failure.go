package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FailPublicationSourceCleanupExplicitRetryDelivery persists the owner-returned
// fence after the sequential retry runner has stopped all further callbacks.
// SQL identity checks cannot prove callback exit; watchdogs must not call this.
func (repository *SetupManagerRepository) FailPublicationSourceCleanupExplicitRetryDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, safeError string) (bool, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil || strings.TrimSpace(safeError) == "" {
		return false, fmt.Errorf("fail explicit publication source cleanup retry delivery: valid guard and safe error are required")
	}
	safeError = strings.TrimSpace(safeError)
	failed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var retry Operation
		if err := tx.NewSelect().Model(&retry).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: lock operation: %w", err)
		}
		var summary publicationSourceCleanupRetryOperationSnapshot
		if err := decodeStrictJSON(retry.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: decode operation snapshot: %w", err)
		}
		if retry.Kind != "retry_publication_sources_v1" || retry.State != "running" || retry.Stage != "cleanup_retry_running" || retry.Attempt != guard.ApplicationAttempt || retry.RiverJobID == nil || *retry.RiverJobID != guard.RiverJobID || summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch || summary.OriginalOperationID == uuid.Nil || summary.OriginalClaimUUID == uuid.Nil {
			return nil
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: lock claim: %w", err)
		}
		if len(claims) != 1 || claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard) {
			return nil
		}

		var grants []SourceCleanupExplicitRetry
		if err := tx.NewSelect().Model(&grants).Where("recovery_operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: lock grant: %w", err)
		}
		if len(grants) != 1 {
			return nil
		}
		grant := grants[0]
		if grant.ConsumedApplicationAttempt == nil || *grant.ConsumedApplicationAttempt != guard.ApplicationAttempt || grant.ConsumedRiverJobID == nil || *grant.ConsumedRiverJobID != guard.RiverJobID || grant.ConsumedClaimUUID == nil || *grant.ConsumedClaimUUID != guard.ClaimUUID || grant.RecoveryOperationID != guard.OperationID || grant.PositionID != guard.PositionID || grant.Epoch != guard.Epoch || grant.OriginalOperationID != summary.OriginalOperationID || grant.OriginalClaimUUID != summary.OriginalClaimUUID {
			return nil
		}
		var frontierExists bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_recovery_state WHERE original_operation_id=?)`, grant.OriginalOperationID).Scan(ctx, &frontierExists); err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: check current frontier: %w", err)
		}
		if grant.FrontierRevision == nil && frontierExists || grant.FrontierRevision != nil && !frontierExists {
			return nil
		}
		if grant.FrontierRevision != nil {
			var revision uuid.UUID
			if err := tx.NewRaw(`SELECT revision FROM source_cleanup_recovery_state WHERE original_operation_id=? FOR UPDATE`, grant.OriginalOperationID).Scan(ctx, &revision); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("fail explicit publication source cleanup retry delivery: read pinned frontier revision: %w", err)
			}
			if revision != *grant.FrontierRevision {
				return nil
			}
		}

		var original Operation
		if err := tx.NewSelect().Model(&original).Where("id=?", summary.OriginalOperationID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: read original operation: %w", err)
		}
		var originalSummary publicationSourceCleanupOperationSnapshot
		if decodeStrictJSON(original.InputSnapshot, &originalSummary) != nil || original.Kind != "cleanup_publication_sources_v1" || original.State != "failed" || original.Stage != "cleanup_recovery_required" || original.Attempt != grant.OriginalApplicationAttempt || original.RiverJobID == nil || *original.RiverJobID != grant.OriginalRiverJobID || originalSummary.Version != 1 || originalSummary.PositionID != guard.PositionID || originalSummary.OutputEpoch != guard.Epoch {
			return nil
		}
		var oldClaim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&oldClaim).Where("operation_id=?", original.ID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: read original claim: %w", err)
		}
		oldGuard := PublicationSourceCleanupDeliveryGuard{OperationID: original.ID, ApplicationAttempt: original.Attempt, RiverJobID: *original.RiverJobID, RiverAttempt: oldClaim.RiverAttempt, ClaimUUID: oldClaim.ClaimUUID, BootUUID: oldClaim.BootUUID, DeliveryToken: oldClaim.DeliveryToken, Epoch: guard.Epoch, PositionID: guard.PositionID}
		if oldGuard.ClaimUUID != grant.OriginalClaimUUID || oldGuard.ClaimUUID != summary.OriginalClaimUUID || oldClaim.State != grantBindingOriginalClaimState(grant.Binding) {
			return nil
		}
		binding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, oldGuard, oldClaim, originalSummary)
		if err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: read retained binding: %w", err)
		}
		matches, err := publicationSourceCleanupRetryBindingMatches(ctx, tx, binding, grant.Binding)
		if err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: compare retained binding: %w", err)
		}
		if !matches {
			return nil
		}
		referencesMatch, err := retryItemReferencesMatch(ctx, tx, binding, grant.ID, original.ID)
		if err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: verify retained references: %w", err)
		}
		if !referencesMatch {
			return nil
		}
		var resolutions []SourceCleanupExplicitRetryResolution
		if err := tx.NewSelect().Model(&resolutions).Where("retry_id=?", grant.ID).For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: lock resolutions: %w", err)
		}
		if len(resolutions) > len(binding.Items) {
			return nil
		}
		for _, resolution := range resolutions {
			if resolution.RetryID != grant.ID || resolution.RecoveryOperationID != guard.OperationID || resolution.ApplicationAttempt != guard.ApplicationAttempt || resolution.RiverJobID != guard.RiverJobID || resolution.ClaimUUID != guard.ClaimUUID {
				return nil
			}
			found := false
			for _, item := range binding.Items {
				if item.ItemID == resolution.ItemID {
					found = true
					break
				}
			}
			if !found || resolution.StartedAt.IsZero() || (resolution.CompletedAt == nil) != (resolution.ResolutionKind == nil) {
				return nil
			}
		}

		claimResult, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='recovery_required' WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=? AND state='executing'`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: mark claim recovery required: %w", err)
		}
		if err := requireRowsAffected(claimResult, 1, "fail explicit publication source cleanup retry claim"); err != nil {
			return err
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='cleanup_retry_recovery_required',finished_at=now(),updated_at=now(),safe_error=? WHERE id=? AND kind='retry_publication_sources_v1' AND state='running' AND stage='cleanup_retry_running' AND attempt=? AND river_job_id=?`, safeError, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail explicit publication source cleanup retry delivery: fail operation: %w", err)
		}
		if err := requireRowsAffected(operationResult, 1, "fail explicit publication source cleanup retry operation"); err != nil {
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

func grantBindingOriginalClaimState(raw json.RawMessage) string {
	var binding publicationSourceCleanupRetryBinding
	if decodeStrictJSON(raw, &binding) != nil {
		return ""
	}
	return binding.OriginalClaimState
}
