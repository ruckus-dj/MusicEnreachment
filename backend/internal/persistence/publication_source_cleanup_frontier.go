package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

type publicationSourceCleanupFrontierProof struct {
	Version                     int                         `json:"version"`
	Original                    json.RawMessage             `json:"original_binding"`
	IncomingItems               []SourceCleanupRecoveryItem `json:"incoming_items"`
	LatestResolutions           []json.RawMessage           `json:"latest_resolutions"`
	FoldedItems                 []SourceCleanupRecoveryItem `json:"folded_items"`
	ReturnedRecoveryOperationID uuid.UUID                   `json:"returned_recovery_operation_id"`
	ReturnedApplicationAttempt  int                         `json:"returned_application_attempt"`
	ReturnedRiverJobID          int64                       `json:"returned_river_job_id"`
	ReturnedRiverAttempt        int                         `json:"returned_river_attempt"`
	ReturnedClaimUUID           uuid.UUID                   `json:"returned_claim_uuid"`
	ReturnedBootUUID            uuid.UUID                   `json:"returned_boot_uuid"`
	ReturnedDeliveryToken       string                      `json:"returned_delivery_token"`
}

// ReplacePublicationSourceCleanupExplicitRetry consumes an exact, owner-returned
// failed recovery delivery and atomically advances the current item frontier and
// admits its successor. River job state is deliberately not used as return proof.
func (repository *SetupManagerRepository) ReplacePublicationSourceCleanupExplicitRetry(ctx context.Context, originalOperationID, expectedFailedRecoveryID uuid.UUID, client RiverInserter) (PublicationSourceCleanupExplicitRetryResult, error) {
	result := PublicationSourceCleanupExplicitRetryResult{}
	if originalOperationID == uuid.Nil || expectedFailedRecoveryID == uuid.Nil || client == nil {
		return result, fmt.Errorf("replace explicit publication source cleanup retry: original operation, expected failed recovery, and River client are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var recovery Operation
		if err := tx.NewSelect().Model(&recovery).Where("id=?", expectedFailedRecoveryID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				result.Blockers = []string{"expected failed recovery is unavailable"}
				return nil
			}
			return fmt.Errorf("replace explicit publication source cleanup retry: lock recovery: %w", err)
		}
		var summary publicationSourceCleanupRetryOperationSnapshot
		if decodeStrictJSON(recovery.InputSnapshot, &summary) != nil || recovery.Kind != "retry_publication_sources_v1" || recovery.State != "failed" || recovery.Stage != "cleanup_retry_recovery_required" || recovery.Attempt <= 0 || recovery.RiverJobID == nil || summary.OriginalOperationID != originalOperationID || summary.Version != 1 || summary.PositionID == uuid.Nil || summary.OutputEpoch == uuid.Nil {
			result.Blockers = []string{"expected recovery is not the exact owner-returned failed delivery"}
			return nil
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", expectedFailedRecoveryID).Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("replace explicit publication source cleanup retry: read returned claim: %w", err)
		}
		if len(claims) != 1 || claims[0].State != "recovery_required" {
			result.Blockers = []string{"failed recovery has no exact returned claim"}
			return nil
		}
		returned := claims[0]
		if returned.ApplicationAttempt != recovery.Attempt || returned.RiverJobID != *recovery.RiverJobID || returned.ClaimUUID == uuid.Nil || returned.BootUUID == uuid.Nil || returned.DeliveryToken == "" {
			result.Blockers = []string{"failed recovery claim identity is incomplete"}
			return nil
		}
		var original Operation
		if err := tx.NewSelect().Model(&original).Where("id=?", originalOperationID).Scan(ctx); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: read original operation: %w", err)
		}
		var originalSummary publicationSourceCleanupOperationSnapshot
		if decodeStrictJSON(original.InputSnapshot, &originalSummary) != nil || original.RiverJobID == nil {
			result.Blockers = []string{"original cleanup operation is unavailable"}
			return nil
		}
		var oldClaim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&oldClaim).Where("operation_id=?", originalOperationID).Scan(ctx); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: read original claim: %w", err)
		}
		oldGuard := PublicationSourceCleanupDeliveryGuard{OperationID: originalOperationID, ApplicationAttempt: original.Attempt, RiverJobID: *original.RiverJobID, RiverAttempt: oldClaim.RiverAttempt, ClaimUUID: oldClaim.ClaimUUID, BootUUID: oldClaim.BootUUID, DeliveryToken: oldClaim.DeliveryToken, Epoch: originalSummary.OutputEpoch, PositionID: originalSummary.PositionID}
		valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, oldGuard, publicationSourceCleanupExplicitRetryReplacementPhase)
		if err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: validate original evidence: %w", err)
		}
		if !valid {
			result.Blockers = []string{"original cleanup evidence or current facts changed"}
			return nil
		}
		// The validator has acquired output/source/current-fact locks in canonical
		// order. Re-lock the returned delivery only after those gates, then verify
		// that the owner-return tuple discovered above did not change.
		var lockedRecovery Operation
		if err := tx.NewSelect().Model(&lockedRecovery).Where("id=?", expectedFailedRecoveryID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				result.Blockers = []string{"returned recovery was already replaced"}
				return nil
			}
			return fmt.Errorf("replace explicit publication source cleanup retry: lock returned recovery: %w", err)
		}
		if lockedRecovery.Attempt != recovery.Attempt || lockedRecovery.RiverJobID == nil || recovery.RiverJobID == nil || *lockedRecovery.RiverJobID != *recovery.RiverJobID || lockedRecovery.State != recovery.State || lockedRecovery.Stage != recovery.Stage || !bytes.Equal(lockedRecovery.InputSnapshot, recovery.InputSnapshot) {
			result.Blockers = []string{"returned recovery changed while acquiring current-evidence locks"}
			return nil
		}
		var lockedReturned []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&lockedReturned).Where("operation_id=?", expectedFailedRecoveryID).For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("replace explicit publication source cleanup retry: lock returned claim: %w", err)
		}
		if len(lockedReturned) != 1 || lockedReturned[0] != returned {
			result.Blockers = []string{"returned recovery claim changed while acquiring current-evidence locks"}
			return nil
		}
		var oldGrants []SourceCleanupExplicitRetry
		if err := tx.NewSelect().Model(&oldGrants).Where("original_operation_id=?", originalOperationID).For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return err
		}
		if len(oldGrants) != 1 || oldGrants[0].RecoveryOperationID != expectedFailedRecoveryID {
			result.Blockers = []string{"expected recovery is not the currently pinned grant"}
			return nil
		}
		oldGrant := oldGrants[0]
		if oldGrant.ConsumedApplicationAttempt == nil || *oldGrant.ConsumedApplicationAttempt != returned.ApplicationAttempt || oldGrant.ConsumedRiverJobID == nil || *oldGrant.ConsumedRiverJobID != returned.RiverJobID || oldGrant.ConsumedClaimUUID == nil || *oldGrant.ConsumedClaimUUID != returned.ClaimUUID {
			result.Blockers = []string{"grant is unconsumed or already pinned to another frontier"}
			return nil
		}
		var frontierExists bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_recovery_state WHERE original_operation_id=?)`, originalOperationID).Scan(ctx, &frontierExists); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: read current frontier: %w", err)
		}
		if oldGrant.FrontierRevision != nil {
			var currentRevision uuid.UUID
			if err := tx.NewRaw(`SELECT revision FROM source_cleanup_recovery_state WHERE original_operation_id=? FOR UPDATE`, originalOperationID).Scan(ctx, &currentRevision); err != nil {
				return fmt.Errorf("replace explicit publication source cleanup retry: read frontier revision: %w", err)
			}
			if currentRevision != *oldGrant.FrontierRevision {
				result.Blockers = []string{"grant frontier revision is stale"}
				return nil
			}
		} else {
			if frontierExists {
				result.Blockers = []string{"grant lacks the current frontier revision"}
				return nil
			}
		}
		if original.State != "failed" || original.Stage != "cleanup_recovery_required" || original.SafeError == nil || *original.SafeError == "" || original.FinishedAt == nil || original.FinishedAt.IsZero() {
			result.Blockers = []string{"original cleanup lacks owner-returned failure proof"}
			return nil
		}
		freshBinding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, oldGuard, oldClaim, originalSummary)
		if err != nil {
			return err
		}
		bindingMatches, err := publicationSourceCleanupRetryBindingMatches(ctx, tx, freshBinding, oldGrant.Binding)
		if err != nil {
			return err
		}
		refsMatch, err := retryItemReferencesMatch(ctx, tx, freshBinding, oldGrant.ID, originalOperationID)
		if err != nil {
			return err
		}
		if !bindingMatches || !refsMatch {
			result.Blockers = []string{"original frozen binding or owner references changed"}
			return nil
		}
		var recoveryItems []SourceCleanupRecoveryItem
		if err := tx.NewSelect().Model(&recoveryItems).Where("original_operation_id=?", originalOperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return err
		}
		var bindings []SourceCleanupExplicitRetryItem
		if err := tx.NewSelect().Model(&bindings).Where("retry_id=?", oldGrant.ID).Order("item_id ASC").Scan(ctx); err != nil {
			return err
		}
		// Seed every original item from the frozen original mutation evidence. A
		// recovery_required item is uncertain even when it has no resolution row.
		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", originalOperationID).Order("id ASC").Scan(ctx); err != nil {
			return err
		}
		if len(items) == 0 || len(bindings) != len(items) || (frontierExists && len(recoveryItems) != len(items)) {
			result.Blockers = []string{"original item binding is incomplete"}
			return nil
		}
		frontier := make(map[uuid.UUID]SourceCleanupRecoveryItem, len(items))
		incoming := make(map[uuid.UUID]SourceCleanupRecoveryItem, len(recoveryItems))
		for _, v := range recoveryItems {
			if _, duplicate := incoming[v.ItemID]; duplicate {
				result.Blockers = []string{"current frontier contains duplicate items"}
				return nil
			}
			incoming[v.ItemID] = v
		}
		bindingByID := make(map[uuid.UUID]SourceCleanupExplicitRetryItem, len(bindings))
		for _, b := range bindings {
			bindingByID[b.ItemID] = b
		}
		for _, item := range items {
			b, bound := bindingByID[item.ID]
			if !bound || b.OriginalOperationID != originalOperationID || b.OwnerUUID == uuid.Nil {
				result.Blockers = []string{"original item binding is incomplete"}
				return nil
			}
			v := SourceCleanupRecoveryItem{OriginalOperationID: originalOperationID, ItemID: item.ID, Kind: "never_started"}
			if item.MutationClaimUUID != nil && *item.MutationClaimUUID == oldGuard.ClaimUUID && item.MutationStartedAt != nil {
				started := *item.MutationStartedAt
				v.Kind, v.StartedAt = "uncertain", &started
				if item.MutationCompletedAt != nil && !item.MutationCompletedAt.Before(started) {
					completed := *item.MutationCompletedAt
					v.Kind, v.CompletedAt = "original_native_completed", &completed
				}
			}
			if frontierExists {
				v, bound = incoming[item.ID]
				if !bound || v.OriginalOperationID != originalOperationID || v.ItemID != item.ID || !validPublicationSourceCleanupFrontierItem(v) {
					result.Blockers = []string{"current frontier does not exactly cover original items"}
					return nil
				}
			}
			var resolution SourceCleanupExplicitRetryResolution
			err := tx.NewSelect().Model(&resolution).Where("retry_id=? AND item_id=?", oldGrant.ID, item.ID).For("UPDATE").Scan(ctx)
			if err == nil {
				if resolution.RecoveryOperationID != expectedFailedRecoveryID || resolution.ApplicationAttempt != returned.ApplicationAttempt || resolution.RiverJobID != returned.RiverJobID || resolution.ClaimUUID != returned.ClaimUUID {
					result.Blockers = []string{"resolution does not match returned recovery tuple"}
					return nil
				}
				if resolution.CompletedAt == nil {
					if v.Kind != "original_native_completed" && v.Kind != "native_removed" && v.Kind != "absent_after_explicit_retry" {
						v.Kind = "uncertain"
						v.StartedAt = &resolution.StartedAt
						v.CompletedAt = nil
					}
				} else if resolution.ResolutionKind != nil {
					kind := *resolution.ResolutionKind
					compatible := v.Kind == "original_native_completed" && kind == "original_native_completed" || v.Kind == "native_removed" && kind == "native_removed" || v.Kind == "absent_after_explicit_retry" && kind == "absent_after_explicit_retry" || v.Kind == "uncertain" && (kind == "native_removed" || kind == "absent_after_explicit_retry") || v.Kind == "never_started" && kind == "native_removed"
					if !compatible {
						result.Blockers = []string{"completed resolution is incompatible with the incoming frontier"}
						return nil
					}
					v.Kind = kind
					started := resolution.StartedAt
					completed := *resolution.CompletedAt
					v.StartedAt = &started
					v.CompletedAt = &completed
				}
			} else if err != sql.ErrNoRows {
				return err
			}
			frontier[item.ID] = v
		}
		// The database function is the canonical transfer proof. It captures the
		// exact incoming frontier/resolution rows and performs the same fold the
		// deferred schema guards verify, while those source rows still exist.
		// Scan the jsonb function result as text; a bare json.RawMessage
		// destination is a byte slice, which Bun treats as a row target and
		// mis-parses the single JSON value as many uint8 rows.
		var proofText string
		if err := tx.NewRaw(`SELECT public.source_cleanup_frontier_transfer_proof(?,?)::text`, originalOperationID, expectedFailedRecoveryID).Scan(ctx, &proofText); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: build canonical frontier proof: %w", err)
		}
		proof := json.RawMessage(proofText)
		var decodedProof publicationSourceCleanupFrontierProof
		if err := decodeStrictJSON(proof, &decodedProof); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: decode canonical frontier proof: %w", err)
		}
		if decodedProof.Version != 1 || len(decodedProof.Original) == 0 || len(decodedProof.FoldedItems) != len(items) {
			return fmt.Errorf("replace explicit publication source cleanup retry: incomplete canonical frontier proof")
		}
		frontier = make(map[uuid.UUID]SourceCleanupRecoveryItem, len(decodedProof.FoldedItems))
		for _, v := range decodedProof.FoldedItems {
			if v.OriginalOperationID != originalOperationID || !validPublicationSourceCleanupFrontierItem(v) {
				result.Blockers = []string{"database returned an invalid folded frontier item"}
				return nil
			}
			if _, duplicate := frontier[v.ItemID]; duplicate {
				result.Blockers = []string{"database returned duplicate folded frontier items"}
				return nil
			}
			frontier[v.ItemID] = v
		}
		for _, item := range items {
			if _, ok := frontier[item.ID]; !ok {
				result.Blockers = []string{"database returned incomplete folded frontier coverage"}
				return nil
			}
		}
		newRevision, newGrantID, newOperationID := uuid.New(), uuid.New(), uuid.New()
		// The old grant is retired only after the exact owner-returned failure was
		// locked and folded above. This releases its revision pin for replacement.
		if _, err := tx.NewRaw(`DELETE FROM source_cleanup_explicit_retry_resolution WHERE retry_id=?`, oldGrant.ID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM source_cleanup_explicit_retry_item WHERE retry_id=?`, oldGrant.ID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM source_cleanup_explicit_retry WHERE id=? AND consumed_claim_uuid=?`, oldGrant.ID, returned.ClaimUUID).Exec(ctx); err != nil {
			return err
		}
		state := SourceCleanupRecoveryState{OriginalOperationID: originalOperationID, Revision: newRevision, ReturnedRecoveryOperationID: expectedFailedRecoveryID,
			ReturnedApplicationAttempt: returned.ApplicationAttempt, ReturnedRiverJobID: returned.RiverJobID, ReturnedRiverAttempt: returned.RiverAttempt,
			ReturnedClaimUUID: returned.ClaimUUID, ReturnedBootUUID: returned.BootUUID, ReturnedDeliveryToken: returned.DeliveryToken, Proof: append(json.RawMessage(nil), proof...)}
		if _, err := tx.NewInsert().Model(&state).On("CONFLICT (original_operation_id) DO UPDATE").Set("revision=EXCLUDED.revision").Set("returned_recovery_operation_id=EXCLUDED.returned_recovery_operation_id").Set("returned_application_attempt=EXCLUDED.returned_application_attempt").Set("returned_river_job_id=EXCLUDED.returned_river_job_id").Set("returned_river_attempt=EXCLUDED.returned_river_attempt").Set("returned_claim_uuid=EXCLUDED.returned_claim_uuid").Set("returned_boot_uuid=EXCLUDED.returned_boot_uuid").Set("returned_delivery_token=EXCLUDED.returned_delivery_token").Set("proof=EXCLUDED.proof").Exec(ctx); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: write current frontier: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM source_cleanup_recovery_item WHERE original_operation_id=?`, originalOperationID).Exec(ctx); err != nil {
			return err
		}
		for _, item := range items {
			v := frontier[item.ID]
			if _, err := tx.NewInsert().Model(&v).Exec(ctx); err != nil {
				return fmt.Errorf("replace explicit publication source cleanup retry: write frontier item: %w", err)
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=?`, expectedFailedRecoveryID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM operation WHERE id=? AND state='failed' AND stage='cleanup_retry_recovery_required'`, expectedFailedRecoveryID).Exec(ctx); err != nil {
			return err
		}
		newSnapshot, err := json.Marshal(publicationSourceCleanupRetryOperationSnapshot{Version: 1, PositionID: oldGrant.PositionID, OutputEpoch: oldGrant.Epoch, OriginalOperationID: originalOperationID, OriginalClaimUUID: oldGrant.OriginalClaimUUID})
		if err != nil {
			return err
		}
		op := &Operation{ID: newOperationID, Kind: "retry_publication_sources_v1", State: "queued", Stage: "queued", Attempt: 1, InputSnapshot: newSnapshot}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, op, client, publicationSourceCleanupRetryJobArgs{OperationID: newOperationID}, nil); err != nil {
			return fmt.Errorf("replace explicit publication source cleanup retry: enqueue successor: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry(id,recovery_operation_id,original_operation_id,original_application_attempt,original_river_job_id,original_claim_uuid,position_id,epoch,publication_generation,binding,frontier_revision) VALUES (?,?,?,?,?,?,?,?,?,?::jsonb,?)`, newGrantID, newOperationID, originalOperationID, oldGrant.OriginalApplicationAttempt, oldGrant.OriginalRiverJobID, oldGrant.OriginalClaimUUID, oldGrant.PositionID, oldGrant.Epoch, oldGrant.PublicationGeneration, string(oldGrant.Binding), newRevision).Exec(ctx); err != nil {
			return err
		}
		for _, ref := range bindings {
			if _, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry_item(retry_id,original_operation_id,item_id,owner_uuid) VALUES (?,?,?,?)`, newGrantID, originalOperationID, ref.ItemID, ref.OwnerUUID).Exec(ctx); err != nil {
				return err
			}
		}
		result.OperationID = newOperationID
		return nil
	})
	if err != nil {
		return PublicationSourceCleanupExplicitRetryResult{}, err
	}
	return result, nil
}

func validPublicationSourceCleanupFrontierItem(item SourceCleanupRecoveryItem) bool {
	switch item.Kind {
	case "never_started":
		return item.StartedAt == nil && item.CompletedAt == nil
	case "uncertain":
		return item.StartedAt != nil && !item.StartedAt.IsZero() && item.CompletedAt == nil
	case "original_native_completed", "native_removed", "absent_after_explicit_retry":
		return item.StartedAt != nil && !item.StartedAt.IsZero() && item.CompletedAt != nil && !item.CompletedAt.Before(*item.StartedAt)
	default:
		return false
	}
}

var _ river.JobArgs = publicationSourceCleanupRetryJobArgs{}
