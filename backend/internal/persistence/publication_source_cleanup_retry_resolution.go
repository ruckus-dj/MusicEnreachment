package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupNativeOutcome is the persistence-owned subset of a
// native remover result needed to settle a durable retry item.
type PublicationSourceCleanupNativeOutcome string

const (
	PublicationSourceCleanupNativeRemoved     PublicationSourceCleanupNativeOutcome = "removed"
	PublicationSourceCleanupNativeMissingLeaf PublicationSourceCleanupNativeOutcome = "missing_leaf"
	PublicationSourceCleanupNativeUnresolved  PublicationSourceCleanupNativeOutcome = "unresolved"
)

type PublicationSourceCleanupRetryResolutionOutcome string

const (
	PublicationSourceCleanupRetryResolutionBlocked          PublicationSourceCleanupRetryResolutionOutcome = "blocked"
	PublicationSourceCleanupRetryResolutionCompleted        PublicationSourceCleanupRetryResolutionOutcome = "completed"
	PublicationSourceCleanupRetryResolutionAlreadyCompleted PublicationSourceCleanupRetryResolutionOutcome = "already_completed"
)

type publicationSourceCleanupRetryResolutionProof struct {
	grant             SourceCleanupExplicitRetry
	oldGuard          PublicationSourceCleanupDeliveryGuard
	itemBinding       publicationSourceCleanupRetryItemBinding
	originalDone      bool
	originalUncertain bool
}

// ResolvePublicationSourceCleanupRetryItem durably starts a one-shot item
// resolution before synchronously invoking the caller's native-removal callback.
// An incomplete start is never replayed; filesystem authority is not inferred
// from a previous callback attempt.
func (repository *SetupManagerRepository) ResolvePublicationSourceCleanupRetryItem(
	ctx context.Context,
	guard PublicationSourceCleanupDeliveryGuard,
	itemID uuid.UUID,
	remove func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error),
) (PublicationSourceCleanupRetryResolutionOutcome, error) {
	blocked := PublicationSourceCleanupRetryResolutionBlocked
	if itemID == uuid.Nil || guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return blocked, fmt.Errorf("resolve explicit publication source cleanup retry item: valid item and delivery guard are required")
	}

	var start time.Time
	var ackOnly, originalDone bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		proof, existing, valid, err := repository.lockPublicationSourceCleanupRetryResolutionProof(ctx, tx, guard, itemID, publicationSourceCleanupExplicitRetryAcknowledgementPhase)
		if err != nil || !valid {
			return err
		}
		if existing != nil {
			if existing.CompletedAt == nil || existing.ResolutionKind == nil || *existing.ResolutionKind == "original_native_completed" && !proof.originalDone || !publicationSourceCleanupRetryResolutionMatches(*existing, proof.grant.ID, itemID, guard) {
				return nil
			}
			ackOnly = true
			return nil
		}
		originalDone = proof.originalDone
		if !originalDone {
			if remove == nil {
				return nil
			}
			// This same full proof with the permission-enforcing phase is the only
			// path to a new native callback.
			if _, _, valid, err := repository.lockPublicationSourceCleanupRetryResolutionProof(ctx, tx, guard, itemID, publicationSourceCleanupExplicitRetryPhase); err != nil || !valid {
				return err
			}
		}
		if err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry_resolution
			(retry_id,item_id,recovery_operation_id,application_attempt,river_job_id,claim_uuid,started_at)
			VALUES (?,?,?,?,?,?,clock_timestamp()) ON CONFLICT (retry_id,item_id) DO NOTHING RETURNING started_at`,
			proof.grant.ID, itemID, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID).Scan(ctx, &start); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("resolve explicit publication source cleanup retry item: record start: %w", err)
		}
		return nil
	})
	if err != nil {
		return blocked, err
	}
	if ackOnly {
		return PublicationSourceCleanupRetryResolutionAlreadyCompleted, nil
	}
	if start.IsZero() {
		return blocked, nil
	}

	var request PublicationSourceCleanupRemovalRequest
	var completed bool
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		phase := publicationSourceCleanupExplicitRetryPhase
		if originalDone {
			phase = publicationSourceCleanupExplicitRetryAcknowledgementPhase
		}
		proof, resolution, valid, err := repository.lockPublicationSourceCleanupRetryResolutionProof(ctx, tx, guard, itemID, phase)
		if err != nil || !valid {
			return err
		}
		if resolution == nil || resolution.CompletedAt != nil || resolution.ResolutionKind != nil || !publicationSourceCleanupRetryResolutionMatches(*resolution, proof.grant.ID, itemID, guard) || !resolution.StartedAt.Equal(start) {
			return nil
		}
		var kind string
		if originalDone {
			kind = "original_native_completed"
		} else {
			var frozen publicationSourceCleanupSnapshot
			if err := decodeStrictJSON(proof.itemBinding.SourceSnapshot, &frozen); err != nil {
				return fmt.Errorf("resolve explicit publication source cleanup retry item: decode frozen source: %w", err)
			}
			mtime, err := time.Parse(time.RFC3339Nano, frozen.Mtime)
			if err != nil || frozen.Version != 1 || !filepath.IsAbs(frozen.ConfiguredPath) || !fs.ValidPath(frozen.RelativePath) || frozen.RelativePath == "." || frozen.SizeBytes < 0 {
				return nil
			}
			request = PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: frozen.ConfiguredPath, RelativePath: frozen.RelativePath, SizeBytes: frozen.SizeBytes, MTime: mtime}
			if remove == nil {
				return nil
			}
			nativeOutcome, callbackErr := remove(ctx, request)
			switch {
			case nativeOutcome == PublicationSourceCleanupNativeRemoved && callbackErr == nil:
				kind = "native_removed"
			case nativeOutcome == PublicationSourceCleanupNativeMissingLeaf && errors.Is(callbackErr, fs.ErrNotExist) && proof.originalUncertain:
				kind = "absent_after_explicit_retry"
			case callbackErr != nil:
				return callbackErr
			default:
				return nil
			}
		}
		result, err := tx.NewRaw(`UPDATE source_cleanup_explicit_retry_resolution
			SET completed_at=clock_timestamp(),resolution_kind=?
			WHERE retry_id=? AND item_id=? AND recovery_operation_id=? AND application_attempt=? AND river_job_id=? AND claim_uuid=?
			AND started_at=? AND completed_at IS NULL AND resolution_kind IS NULL`, kind, proof.grant.ID, itemID,
			guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID, start).Exec(ctx)
		if err != nil {
			return fmt.Errorf("resolve explicit publication source cleanup retry item: record completion: %w", err)
		}
		if err := requireRowsAffected(result, 1, "complete explicit publication source cleanup retry item resolution"); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		return blocked, err
	}
	if !completed {
		return blocked, nil
	}
	return PublicationSourceCleanupRetryResolutionCompleted, nil
}

func (repository *SetupManagerRepository) lockPublicationSourceCleanupRetryResolutionProof(
	ctx context.Context,
	tx bun.Tx,
	guard PublicationSourceCleanupDeliveryGuard,
	itemID uuid.UUID,
	phase publicationSourceCleanupExecutionPhase,
) (publicationSourceCleanupRetryResolutionProof, *SourceCleanupExplicitRetryResolution, bool, error) {
	var proof publicationSourceCleanupRetryResolutionProof
	var discovered Operation
	if err := tx.NewSelect().Model(&discovered).Where("id=?", guard.OperationID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proof, nil, false, nil
		}
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: discover retry operation: %w", err)
	}
	var retrySummary publicationSourceCleanupRetryOperationSnapshot
	if decodeStrictJSON(discovered.InputSnapshot, &retrySummary) != nil || discovered.Kind != "retry_publication_sources_v1" || discovered.Attempt != guard.ApplicationAttempt || discovered.RiverJobID == nil || *discovered.RiverJobID != guard.RiverJobID || retrySummary.Version != 1 || retrySummary.PositionID != guard.PositionID || retrySummary.OutputEpoch != guard.Epoch {
		return proof, nil, false, nil
	}
	var original Operation
	if err := tx.NewSelect().Model(&original).Where("id=?", retrySummary.OriginalOperationID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proof, nil, false, nil
		}
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: discover original operation: %w", err)
	}
	var originalSummary publicationSourceCleanupOperationSnapshot
	if decodeStrictJSON(original.InputSnapshot, &originalSummary) != nil || original.RiverJobID == nil {
		return proof, nil, false, nil
	}
	var oldClaim PublicationExecutionClaim
	if err := tx.NewSelect().Model(&oldClaim).Where("operation_id=?", original.ID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proof, nil, false, nil
		}
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: discover original claim: %w", err)
	}
	proof.oldGuard = PublicationSourceCleanupDeliveryGuard{OperationID: original.ID, ApplicationAttempt: original.Attempt,
		RiverJobID: *original.RiverJobID, RiverAttempt: oldClaim.RiverAttempt, ClaimUUID: oldClaim.ClaimUUID,
		BootUUID: oldClaim.BootUUID, DeliveryToken: oldClaim.DeliveryToken, Epoch: originalSummary.OutputEpoch, PositionID: originalSummary.PositionID}
	if retrySummary.OriginalOperationID != original.ID || retrySummary.OriginalClaimUUID != proof.oldGuard.ClaimUUID || proof.oldGuard.ClaimUUID == guard.ClaimUUID || proof.oldGuard.Epoch != guard.Epoch || proof.oldGuard.PositionID != guard.PositionID {
		return proof, nil, false, nil
	}
	valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, proof.oldGuard, phase)
	if err != nil || !valid {
		return proof, nil, false, err
	}
	var retry Operation
	if err := tx.NewSelect().Model(&retry).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proof, nil, false, nil
		}
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: lock retry operation: %w", err)
	}
	if retry.State != "running" || retry.Stage != "cleanup_retry_running" || retry.Attempt != guard.ApplicationAttempt || retry.RiverJobID == nil || *retry.RiverJobID != guard.RiverJobID {
		return proof, nil, false, nil
	}
	var currentRetrySummary publicationSourceCleanupRetryOperationSnapshot
	if decodeStrictJSON(retry.InputSnapshot, &currentRetrySummary) != nil || currentRetrySummary != retrySummary {
		return proof, nil, false, nil
	}
	var riverKind, riverState string
	var riverAttempt int
	var args json.RawMessage
	if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &args); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proof, nil, false, nil
		}
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: lock new River job: %w", err)
	}
	var jobArgs publicationSourceCleanupRetryJobArgs
	if decodeStrictJSON(args, &jobArgs) != nil || riverKind != jobArgs.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || jobArgs.OperationID != guard.OperationID {
		return proof, nil, false, nil
	}
	var claims []PublicationExecutionClaim
	if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: lock new claims: %w", err)
	}
	if len(claims) != 1 || claims[0].State != "executing" || !publicationSourceCleanupClaimMatches(claims[0], guard) {
		return proof, nil, false, nil
	}
	var grants []SourceCleanupExplicitRetry
	if err := tx.NewSelect().Model(&grants).Where("recovery_operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: lock grant: %w", err)
	}
	if len(grants) != 1 {
		return proof, nil, false, nil
	}
	proof.grant = grants[0]
	grant := proof.grant
	if grant.OriginalOperationID != proof.oldGuard.OperationID || grant.OriginalApplicationAttempt != proof.oldGuard.ApplicationAttempt || grant.OriginalRiverJobID != proof.oldGuard.RiverJobID || grant.OriginalClaimUUID != proof.oldGuard.ClaimUUID || grant.PositionID != guard.PositionID || grant.Epoch != guard.Epoch || grant.ConsumedApplicationAttempt == nil || *grant.ConsumedApplicationAttempt != guard.ApplicationAttempt || grant.ConsumedRiverJobID == nil || *grant.ConsumedRiverJobID != guard.RiverJobID || grant.ConsumedClaimUUID == nil || *grant.ConsumedClaimUUID != guard.ClaimUUID {
		return proof, nil, false, nil
	}
	freshBinding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, proof.oldGuard, oldClaim, originalSummary)
	if err != nil {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: reread retained binding: %w", err)
	}
	same, err := publicationSourceCleanupRetryBindingMatches(ctx, tx, freshBinding, grant.Binding)
	if err != nil {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: compare retained binding: %w", err)
	}
	if !same {
		return proof, nil, false, nil
	}
	referencesMatch, err := retryItemReferencesMatch(ctx, tx, freshBinding, grant.ID, proof.oldGuard.OperationID)
	if err != nil {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: verify item references: %w", err)
	}
	if !referencesMatch {
		return proof, nil, false, nil
	}
	for _, item := range freshBinding.Items {
		if item.ItemID != itemID {
			continue
		}
		proof.itemBinding = item
		proof.originalDone = item.State == "claimed" && item.MutationClaimUUID != nil && *item.MutationClaimUUID == proof.oldGuard.ClaimUUID && item.MutationStartedAt != nil && item.MutationCompletedAt != nil
		proof.originalUncertain = item.State == "recovery_required" && item.MutationClaimUUID != nil && *item.MutationClaimUUID == proof.oldGuard.ClaimUUID && item.MutationStartedAt != nil && item.MutationCompletedAt == nil
		break
	}
	if proof.itemBinding.ItemID != itemID {
		return proof, nil, false, nil
	}
	var resolution SourceCleanupExplicitRetryResolution
	err = tx.NewSelect().Model(&resolution).Where("retry_id=? AND item_id=?", grant.ID, itemID).For("UPDATE").Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return proof, nil, false, fmt.Errorf("resolve explicit publication source cleanup retry item: lock resolution: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return proof, nil, true, nil
	}
	return proof, &resolution, true, nil
}

func publicationSourceCleanupRetryResolutionMatches(resolution SourceCleanupExplicitRetryResolution, retryID, itemID uuid.UUID, guard PublicationSourceCleanupDeliveryGuard) bool {
	return resolution.RetryID == retryID && resolution.ItemID == itemID && resolution.RecoveryOperationID == guard.OperationID && resolution.ApplicationAttempt == guard.ApplicationAttempt && resolution.RiverJobID == guard.RiverJobID && resolution.ClaimUUID == guard.ClaimUUID
}
