package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ClaimPublicationSourceCleanupExplicitRetry claims the new retry delivery and
// consumes its grant atomically. This records database ownership only; it does
// not perform cleanup or grant filesystem authority.
func (repository *SetupManagerRepository) ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard) (PublicationDeliveryClaimOutcome, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return PublicationDeliveryBlocked, errors.New("invalid publication source cleanup explicit retry delivery guard")
	}
	outcome := PublicationDeliveryBlocked
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Discover the retry snapshot and original retained identity without taking
		// locks. The phase validator below establishes canonical source/namespace locks.
		var discoveredRetry Operation
		if err := tx.NewSelect().Model(&discoveredRetry).Where("id=?", guard.OperationID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim explicit publication source cleanup retry: discover retry operation: %w", err)
		}
		var discoveredSummary publicationSourceCleanupRetryOperationSnapshot
		if decodeStrictJSON(discoveredRetry.InputSnapshot, &discoveredSummary) != nil || discoveredRetry.Kind != "retry_publication_sources_v1" || discoveredRetry.Attempt != guard.ApplicationAttempt || discoveredRetry.RiverJobID == nil || *discoveredRetry.RiverJobID != guard.RiverJobID || discoveredSummary.Version != 1 || discoveredSummary.PositionID != guard.PositionID || discoveredSummary.OutputEpoch != guard.Epoch || discoveredSummary.OriginalOperationID == uuid.Nil || discoveredSummary.OriginalClaimUUID == uuid.Nil {
			return nil
		}
		originalOperationID := discoveredSummary.OriginalOperationID
		var original Operation
		if err := tx.NewSelect().Model(&original).Where("id=?", originalOperationID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim explicit publication source cleanup retry: discover original operation: %w", err)
		}
		var originalSummary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(original.InputSnapshot, &originalSummary); err != nil {
			return nil
		}
		if original.RiverJobID == nil || original.Attempt <= 0 || originalSummary.Version != 1 || originalSummary.PositionID == uuid.Nil || originalSummary.OutputEpoch == uuid.Nil {
			return nil
		}
		var oldClaim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&oldClaim).Where("operation_id=?", original.ID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim explicit publication source cleanup retry: discover original claim: %w", err)
		}
		oldGuard := PublicationSourceCleanupDeliveryGuard{OperationID: original.ID, ApplicationAttempt: original.Attempt,
			RiverJobID: *original.RiverJobID, RiverAttempt: oldClaim.RiverAttempt, ClaimUUID: oldClaim.ClaimUUID,
			BootUUID: oldClaim.BootUUID, DeliveryToken: oldClaim.DeliveryToken,
			Epoch: originalSummary.OutputEpoch, PositionID: originalSummary.PositionID}
		if oldGuard.ClaimUUID != discoveredSummary.OriginalClaimUUID || oldGuard.ClaimUUID == guard.ClaimUUID || guard.Epoch != oldGuard.Epoch || guard.PositionID != oldGuard.PositionID {
			return nil
		}
		valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, oldGuard, publicationSourceCleanupExplicitRetryPhase)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: validate returned original delivery: %w", err)
		}
		if !valid {
			return nil
		}

		var newOperation Operation
		if err := tx.NewSelect().Model(&newOperation).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim explicit publication source cleanup retry: lock retry operation: %w", err)
		}
		var newSummary publicationSourceCleanupRetryOperationSnapshot
		if err := decodeStrictJSON(newOperation.InputSnapshot, &newSummary); err != nil {
			return nil
		}
		if newOperation.Kind != "retry_publication_sources_v1" || newOperation.Attempt != guard.ApplicationAttempt || newOperation.RiverJobID == nil || *newOperation.RiverJobID != guard.RiverJobID || newSummary.Version != 1 || newSummary.PositionID != guard.PositionID || newSummary.OutputEpoch != guard.Epoch || newSummary.OriginalOperationID != oldGuard.OperationID || newSummary.OriginalClaimUUID != oldGuard.ClaimUUID {
			return nil
		}
		var grants []SourceCleanupExplicitRetry
		if err := tx.NewSelect().Model(&grants).Where("original_operation_id=? AND original_claim_uuid=?", oldGuard.OperationID, oldGuard.ClaimUUID).For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim explicit publication source cleanup retry: lock grant: %w", err)
		}
		if len(grants) != 1 {
			return nil
		}
		grant := grants[0]
		if grant.RecoveryOperationID != guard.OperationID || grant.OriginalOperationID != oldGuard.OperationID || grant.OriginalApplicationAttempt != oldGuard.ApplicationAttempt || grant.OriginalRiverJobID != oldGuard.RiverJobID || grant.OriginalClaimUUID != oldGuard.ClaimUUID || grant.PositionID != guard.PositionID || grant.Epoch != guard.Epoch {
			return nil
		}
		freshBinding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, oldGuard, oldClaim, originalSummary)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: reread original binding: %w", err)
		}
		freshJSON, err := json.Marshal(freshBinding)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: encode fresh binding: %w", err)
		}
		var sameBinding bool
		if err := tx.NewRaw(`SELECT ?::jsonb = ?::jsonb`, string(freshJSON), string(grant.Binding)).Scan(ctx, &sameBinding); err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: compare binding: %w", err)
		}
		if !sameBinding {
			return nil
		}
		referencesMatch, err := retryItemReferencesMatch(ctx, tx, freshBinding, grant.ID, oldGuard.OperationID)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: verify retained item references: %w", err)
		}
		if !referencesMatch {
			return nil
		}

		var riverKind, riverState string
		var riverAttempt int
		var args json.RawMessage
		if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &args); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim explicit publication source cleanup retry: lock River job: %w", err)
		}
		var jobArgs publicationSourceCleanupRetryJobArgs
		if err := decodeStrictJSON(args, &jobArgs); err != nil {
			return nil
		}
		if riverKind != jobArgs.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || jobArgs.OperationID != guard.OperationID || jobArgs.OperationID == uuid.Nil {
			return nil
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim explicit publication source cleanup retry: lock retry claims: %w", err)
		}
		consumed := grant.ConsumedApplicationAttempt != nil && grant.ConsumedRiverJobID != nil && grant.ConsumedClaimUUID != nil
		if consumed {
			if *grant.ConsumedApplicationAttempt == guard.ApplicationAttempt && *grant.ConsumedRiverJobID == guard.RiverJobID && *grant.ConsumedClaimUUID == guard.ClaimUUID && newOperation.State == "running" && len(claims) == 1 && claims[0].State == "executing" && publicationSourceCleanupClaimMatches(claims[0], guard) {
				outcome = PublicationDeliveryAlreadyOwned
			}
			return nil
		}
		if grant.ConsumedApplicationAttempt != nil || grant.ConsumedRiverJobID != nil || grant.ConsumedClaimUUID != nil || newOperation.State != "queued" || newOperation.Stage != "queued" || len(claims) != 0 {
			return nil
		}
		claim := PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt, ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "executing"}
		if _, err := tx.NewInsert().Model(&claim).Exec(ctx); err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: insert retry claim: %w", err)
		}
		consumedResult, err := tx.NewRaw(`UPDATE source_cleanup_explicit_retry SET consumed_application_attempt=?,consumed_river_job_id=?,consumed_claim_uuid=? WHERE id=? AND consumed_application_attempt IS NULL AND consumed_river_job_id IS NULL AND consumed_claim_uuid IS NULL`, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID, grant.ID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: consume grant: %w", err)
		}
		if err := requireRowsAffected(consumedResult, 1, "consume explicit publication source cleanup retry grant"); err != nil {
			return err
		}
		started, err := tx.NewRaw(`UPDATE operation SET state='running',stage='cleanup_retry_running',started_at=now(),updated_at=now() WHERE id=? AND kind='retry_publication_sources_v1' AND state='queued' AND stage='queued' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim explicit publication source cleanup retry: start retry operation: %w", err)
		}
		if err := requireRowsAffected(started, 1, "start explicit publication source cleanup retry operation"); err != nil {
			return err
		}
		outcome = PublicationDeliveryAcquired
		return nil
	})
	if err != nil {
		return PublicationDeliveryBlocked, err
	}
	return outcome, nil
}

func retryItemReferencesMatch(ctx context.Context, tx bun.IDB, binding publicationSourceCleanupRetryBinding, grantID, originalOperationID uuid.UUID) (bool, error) {
	var references []SourceCleanupExplicitRetryItem
	if err := tx.NewSelect().Model(&references).Where("retry_id=? AND original_operation_id=?", grantID, originalOperationID).Order("item_id ASC").Scan(ctx); err != nil {
		return false, err
	}
	if len(references) != len(binding.Items) {
		return false, nil
	}
	items := append([]publicationSourceCleanupRetryItemBinding(nil), binding.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ItemID.String() < items[j].ItemID.String() })
	for i, reference := range references {
		if reference.RetryID != grantID || reference.OriginalOperationID != originalOperationID || reference.ItemID != items[i].ItemID || reference.OwnerUUID != items[i].OwnerUUID {
			return false, nil
		}
	}
	return true, nil
}
