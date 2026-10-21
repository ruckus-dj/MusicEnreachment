package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SettlePublicationSourceCleanupExplicitRetry retires a fully resolved explicit
// retry and its retained cleanup evidence without changing source inventory.
func (repository *SetupManagerRepository) SettlePublicationSourceCleanupExplicitRetry(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard) (bool, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return false, fmt.Errorf("settle explicit publication source cleanup retry: invalid delivery guard")
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var grants []SourceCleanupExplicitRetry
		if err := tx.NewSelect().Model(&grants).Where("recovery_operation_id=?", guard.OperationID).Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("settle explicit publication source cleanup retry: read grant: %w", err)
		}
		if len(grants) != 1 {
			return nil
		}
		var refs []SourceCleanupExplicitRetryItem
		if err := tx.NewSelect().Model(&refs).Where("retry_id=?", grants[0].ID).Order("item_id ASC").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("settle explicit publication source cleanup retry: read item references: %w", err)
		}
		if len(refs) == 0 {
			return nil
		}
		proof, _, valid, err := repository.lockPublicationSourceCleanupRetryResolutionProof(ctx, tx, guard, refs[0].ItemID, publicationSourceCleanupExplicitRetryAcknowledgementPhase)
		if err != nil || !valid {
			return err
		}
		grant := proof.grant
		// Rebuild binding from the locked, authoritative rows using the same
		// identity used by claim and resolution admission.
		var oldClaim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&oldClaim).Where("operation_id=?", proof.oldGuard.OperationID).Scan(ctx); err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: read original claim: %w", err)
		}
		var original Operation
		if err := tx.NewSelect().Model(&original).Where("id=?", proof.oldGuard.OperationID).Scan(ctx); err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: read original operation: %w", err)
		}
		var originalSummary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(original.InputSnapshot, &originalSummary); err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: decode original snapshot: %w", err)
		}
		binding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, proof.oldGuard, oldClaim, originalSummary)
		if err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: reread binding: %w", err)
		}
		same, err := publicationSourceCleanupRetryBindingMatches(ctx, tx, binding, grant.Binding)
		if err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: compare binding: %w", err)
		}
		if !same || !originalUnsettledCleanupMatches(original, proof.oldGuard) {
			return nil
		}
		if len(refs) != len(binding.Items) {
			return nil
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].ItemID.String() < refs[j].ItemID.String() })
		items := append([]publicationSourceCleanupRetryItemBinding(nil), binding.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].ItemID.String() < items[j].ItemID.String() })
		for i, ref := range refs {
			if ref.RetryID != grant.ID || ref.OriginalOperationID != proof.oldGuard.OperationID || ref.ItemID != items[i].ItemID || ref.OwnerUUID != items[i].OwnerUUID {
				return nil
			}
		}
		var resolutions []SourceCleanupExplicitRetryResolution
		if err := tx.NewSelect().Model(&resolutions).Where("retry_id=?", grant.ID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("settle explicit publication source cleanup retry: lock resolutions: %w", err)
		}
		if len(resolutions) != len(items) {
			return nil
		}
		frontierKinds := map[uuid.UUID]SourceCleanupRecoveryItem{}
		if grant.FrontierRevision != nil {
			var revision uuid.UUID
			if err := tx.NewRaw(`SELECT revision FROM source_cleanup_recovery_state WHERE original_operation_id=? FOR UPDATE`, proof.oldGuard.OperationID).Scan(ctx, &revision); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("settle explicit publication source cleanup retry: read pinned frontier revision: %w", err)
			}
			if revision != *grant.FrontierRevision {
				return nil
			}
			var current []SourceCleanupRecoveryItem
			if err := tx.NewSelect().Model(&current).Where("original_operation_id=?", proof.oldGuard.OperationID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("settle explicit publication source cleanup retry: read current frontier items: %w", err)
			}
			for _, v := range current {
				frontierKinds[v.ItemID] = v
			}
		}
		for i, resolution := range resolutions {
			item := items[i]
			validKind := false
			if frontier, ok := frontierKinds[item.ItemID]; ok {
				switch frontier.Kind {
				case "never_started":
					validKind = resolution.ResolutionKind != nil && *resolution.ResolutionKind == "native_removed"
				case "uncertain":
					validKind = resolution.ResolutionKind != nil && (*resolution.ResolutionKind == "native_removed" || *resolution.ResolutionKind == "absent_after_explicit_retry")
				case "native_removed", "absent_after_explicit_retry", "original_native_completed":
					validKind = resolution.ResolutionKind != nil && *resolution.ResolutionKind == frontier.Kind
				}
			} else {
				validKind = resolution.ResolutionKind != nil && validRetryResolutionKind(*resolution.ResolutionKind, item)
			}
			if resolution.ItemID != item.ItemID || !publicationSourceCleanupRetryResolutionMatches(resolution, grant.ID, item.ItemID, guard) || resolution.CompletedAt == nil || resolution.CompletedAt.IsZero() || !validKind {
				return nil
			}
		}
		var retry Operation
		if err := tx.NewSelect().Model(&retry).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("settle explicit publication source cleanup retry: lock retry operation: %w", err)
		}
		if retry.Kind != "retry_publication_sources_v1" || retry.State != "running" || retry.Stage != "cleanup_retry_running" || retry.Attempt != guard.ApplicationAttempt || retry.RiverJobID == nil || *retry.RiverJobID != guard.RiverJobID {
			return nil
		}
		if err := terminalizeRetryAndDeleteEvidence(ctx, tx, guard, proof.oldGuard, grant, refs, resolutions, original); err != nil {
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

func originalUnsettledCleanupMatches(operation Operation, guard PublicationSourceCleanupDeliveryGuard) bool {
	return operation.Kind == "cleanup_publication_sources_v1" && operation.State == "failed" && operation.Stage == "cleanup_recovery_required" && operation.Attempt == guard.ApplicationAttempt && operation.RiverJobID != nil && *operation.RiverJobID == guard.RiverJobID && operation.SafeError != nil && *operation.SafeError != "" && operation.FinishedAt != nil && !operation.FinishedAt.IsZero()
}

func validRetryResolutionKind(kind string, item publicationSourceCleanupRetryItemBinding) bool {
	switch kind {
	case "original_native_completed":
		started, startedOK := retryMutationTime(item.MutationStartedAt)
		completed, completedOK := retryMutationTime(item.MutationCompletedAt)
		return item.State == "claimed" && item.MutationClaimUUID != nil && item.MutationCompletedAt != nil && *item.MutationClaimUUID != uuid.Nil && startedOK && completedOK && !completed.Before(started)
	case "absent_after_explicit_retry":
		_, startedOK := retryMutationTime(item.MutationStartedAt)
		return item.State == "recovery_required" && item.MutationClaimUUID != nil && item.MutationCompletedAt == nil && *item.MutationClaimUUID != uuid.Nil && startedOK
	case "native_removed":
		return item.State == "claimed" && item.MutationClaimUUID == nil && item.MutationStartedAt == nil && item.MutationCompletedAt == nil
	default:
		return false
	}
}

func retryMutationTime(value *string) (time.Time, bool) {
	if value == nil || *value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	return parsed, err == nil
}

func terminalizeRetryAndDeleteEvidence(ctx context.Context, tx bun.Tx, guard, oldGuard PublicationSourceCleanupDeliveryGuard, grant SourceCleanupExplicitRetry, refs []SourceCleanupExplicitRetryItem, resolutions []SourceCleanupExplicitRetryResolution, original Operation) error {
	var frontierItems int64
	if grant.FrontierRevision != nil {
		if err := tx.NewRaw(`SELECT count(*) FROM source_cleanup_recovery_item WHERE original_operation_id=?`, oldGuard.OperationID).Scan(ctx, &frontierItems); err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: count current frontier: %w", err)
		}
	}
	update, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='finished',finished_at=now(),updated_at=now(),safe_error=NULL WHERE id=? AND state='running' AND stage='cleanup_retry_running' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("settle explicit publication source cleanup retry: terminalize retry: %w", err)
	}
	if err := requireRowsAffected(update, 1, "terminalize explicit publication source cleanup retry"); err != nil {
		return err
	}
	deleteBy := []struct {
		query, label string
		args         []any
	}{
		{`DELETE FROM source_cleanup_explicit_retry_resolution WHERE retry_id=?`, "delete retry resolutions", []any{grant.ID}},
		{`DELETE FROM source_cleanup_explicit_retry_item WHERE retry_id=? AND original_operation_id=?`, "delete retry item references", []any{grant.ID, oldGuard.OperationID}},
		{`DELETE FROM source_cleanup_explicit_retry WHERE id=? AND recovery_operation_id=?`, "delete retry grant", []any{grant.ID, guard.OperationID}},
		{`DELETE FROM source_cleanup_owner WHERE operation_id=?`, "delete original cleanup owners", []any{oldGuard.OperationID}},
		{`DELETE FROM source_cleanup_recovery_item WHERE original_operation_id=?`, "delete current cleanup frontier items", []any{oldGuard.OperationID}},
		{`DELETE FROM source_cleanup_recovery_state WHERE original_operation_id=?`, "delete current cleanup frontier", []any{oldGuard.OperationID}},
		{`DELETE FROM source_cleanup_item WHERE operation_id=?`, "delete original cleanup items", []any{oldGuard.OperationID}},
		{`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=?`, "delete original execution claim", []any{oldGuard.OperationID, oldGuard.ApplicationAttempt, oldGuard.RiverJobID, oldGuard.RiverAttempt, oldGuard.ClaimUUID, oldGuard.BootUUID, oldGuard.DeliveryToken, oldGuard.Epoch}},
		{`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND epoch=?`, "delete retry execution claim", []any{guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken, guard.Epoch}},
		{`UPDATE operation SET state='succeeded',stage='finished',finished_at=now(),updated_at=now(),safe_error=NULL WHERE id=? AND kind='cleanup_publication_sources_v1' AND state='failed' AND stage='cleanup_recovery_required' AND attempt=? AND river_job_id=? AND input_snapshot=?`, "settle original cleanup operation", []any{original.ID, oldGuard.ApplicationAttempt, oldGuard.RiverJobID, original.InputSnapshot}},
		{`DELETE FROM operation WHERE id=? AND kind='retry_publication_sources_v1' AND state='succeeded' AND stage='finished' AND attempt=? AND river_job_id=?`, "delete settled retry operation", []any{guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID}},
	}
	frontierStates := int64(0)
	if grant.FrontierRevision != nil {
		frontierStates = 1
	}
	expected := []int64{int64(len(resolutions)), int64(len(refs)), 1, int64(len(refs)), frontierItems, frontierStates, int64(len(refs)), 1, 1, 1, 1}
	for i, statement := range deleteBy {
		result, err := tx.NewRaw(statement.query, statement.args...).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle explicit publication source cleanup retry: %s: %w", statement.label, err)
		}
		if err := requireRowsAffected(result, expected[i], statement.label); err != nil {
			return err
		}
	}
	return nil
}
