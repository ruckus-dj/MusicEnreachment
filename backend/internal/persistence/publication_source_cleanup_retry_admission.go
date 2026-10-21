package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupExplicitRetryResult contains an admitted recovery
// operation or blockers. This repository method is the explicit-retry boundary;
// no automatic cleanup delivery invokes it.
type PublicationSourceCleanupExplicitRetryResult struct {
	OperationID uuid.UUID
	Blockers    []string
}

type publicationSourceCleanupRetryJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (publicationSourceCleanupRetryJobArgs) Kind() string { return "retry_publication_sources_v1" }

type publicationSourceCleanupRetryOperationSnapshot struct {
	Version             int       `json:"version"`
	PositionID          uuid.UUID `json:"position_id"`
	OutputEpoch         uuid.UUID `json:"output_epoch"`
	OriginalOperationID uuid.UUID `json:"original_operation_id"`
	OriginalClaimUUID   uuid.UUID `json:"original_claim_uuid"`
}

type publicationSourceCleanupRetryBinding struct {
	Version                    int                                        `json:"version"`
	OriginalOperationID        uuid.UUID                                  `json:"original_operation_id"`
	OriginalApplicationAttempt int                                        `json:"original_application_attempt"`
	OriginalRiverJobID         int64                                      `json:"original_river_job_id"`
	OriginalRiverAttempt       int                                        `json:"original_river_attempt"`
	OriginalRiverKind          string                                     `json:"original_river_kind"`
	OriginalClaimUUID          uuid.UUID                                  `json:"original_claim_uuid"`
	OriginalBootUUID           uuid.UUID                                  `json:"original_boot_uuid"`
	OriginalDeliveryToken      string                                     `json:"original_delivery_token"`
	OriginalClaimState         string                                     `json:"original_claim_state"`
	Epoch                      uuid.UUID                                  `json:"epoch"`
	PositionID                 uuid.UUID                                  `json:"position_id"`
	PublicationGeneration      uuid.UUID                                  `json:"publication_generation"`
	Items                      []publicationSourceCleanupRetryItemBinding `json:"items"`
}

type publicationSourceCleanupRetryItemBinding struct {
	ItemID              uuid.UUID       `json:"item_id"`
	State               string          `json:"state"`
	SafeError           *string         `json:"safe_error"`
	SourceSnapshot      json.RawMessage `json:"source_snapshot"`
	BorrowerRevision    uuid.UUID       `json:"borrower_revision"`
	MutationClaimUUID   *uuid.UUID      `json:"mutation_claim_uuid"`
	MutationStartedAt   *string         `json:"mutation_started_at"`
	MutationCompletedAt *string         `json:"mutation_completed_at"`
	OwnerUUID           uuid.UUID       `json:"owner_uuid"`
	SourceRootID        uuid.UUID       `json:"source_root_id"`
	SourceLocationID    uuid.UUID       `json:"source_location_id"`
	WorkID              uuid.UUID       `json:"work_id"`
}

// AdmitPublicationSourceCleanupExplicitRetry grants a new, separately fenced
// operation only after the original delivery's owner has persisted its exact
// recovery_required transition. River state/attempt is not used as an owner-
// return signal: failure persistence occurs after the synchronous callback exits.
func (repository *SetupManagerRepository) AdmitPublicationSourceCleanupExplicitRetry(ctx context.Context, originalOperationID uuid.UUID, client RiverInserter) (PublicationSourceCleanupExplicitRetryResult, error) {
	result := PublicationSourceCleanupExplicitRetryResult{}
	if originalOperationID == uuid.Nil || client == nil {
		return result, fmt.Errorf("admit explicit publication source cleanup retry: original operation and River client are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// This unlocked read only discovers the lock set and guard. The phase
		// validator below re-reads every identity after acquiring canonical gates.
		var original Operation
		if err := tx.NewSelect().Model(&original).Where("id=?", originalOperationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				result.Blockers = []string{"original cleanup operation is unavailable"}
				return nil
			}
			return fmt.Errorf("admit explicit publication source cleanup retry: discover original operation: %w", err)
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(original.InputSnapshot, &summary); err != nil {
			result.Blockers = []string{"original cleanup operation snapshot is invalid"}
			return nil
		}
		if original.RiverJobID == nil || original.Attempt <= 0 || summary.Version != 1 || summary.PositionID == uuid.Nil || summary.OutputEpoch == uuid.Nil {
			result.Blockers = []string{"original cleanup operation identity is incomplete"}
			return nil
		}
		var claim PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claim).Where("operation_id=?", originalOperationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				result.Blockers = []string{"original recovery claim is unavailable"}
				return nil
			}
			return fmt.Errorf("admit explicit publication source cleanup retry: discover original claim: %w", err)
		}
		guard := PublicationSourceCleanupDeliveryGuard{
			OperationID: originalOperationID, ApplicationAttempt: original.Attempt, RiverJobID: *original.RiverJobID,
			RiverAttempt: claim.RiverAttempt, ClaimUUID: claim.ClaimUUID, BootUUID: claim.BootUUID,
			DeliveryToken: claim.DeliveryToken, Epoch: summary.OutputEpoch, PositionID: summary.PositionID,
		}
		valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, guard, publicationSourceCleanupExplicitRetryPhase)
		if err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: validate retained evidence: %w", err)
		}
		if !valid {
			result.Blockers = []string{"original cleanup delivery is not owner-returned with unchanged current evidence"}
			return nil
		}
		var duplicate bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_explicit_retry WHERE original_operation_id=? AND original_claim_uuid=?)`, originalOperationID, claim.ClaimUUID).Scan(ctx, &duplicate); err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: check existing grant: %w", err)
		}
		if duplicate {
			result.Blockers = []string{"original cleanup delivery already has an explicit retry grant"}
			return nil
		}
		binding, err := readPublicationSourceCleanupRetryBinding(ctx, tx, guard, claim, summary)
		if err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: capture binding: %w", err)
		}
		encodedBinding, err := json.Marshal(binding)
		if err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: encode binding: %w", err)
		}
		recoveryOperationID, grantID := uuid.New(), uuid.New()
		snapshot, err := json.Marshal(publicationSourceCleanupRetryOperationSnapshot{
			Version: 1, PositionID: summary.PositionID, OutputEpoch: summary.OutputEpoch,
			OriginalOperationID: originalOperationID, OriginalClaimUUID: claim.ClaimUUID,
		})
		if err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: encode operation snapshot: %w", err)
		}
		recoveryOperation := &Operation{ID: recoveryOperationID, Kind: "retry_publication_sources_v1", State: "queued", Stage: "queued", Attempt: 1, InputSnapshot: snapshot}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, recoveryOperation, client, publicationSourceCleanupRetryJobArgs{OperationID: recoveryOperationID}, nil); err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: enqueue recovery operation: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry
			(id,recovery_operation_id,original_operation_id,original_application_attempt,original_river_job_id,original_claim_uuid,position_id,epoch,publication_generation,binding)
			VALUES (?,?,?,?,?,?,?,?,?,?::jsonb)`, grantID, recoveryOperationID, originalOperationID, original.Attempt, *original.RiverJobID,
			claim.ClaimUUID, summary.PositionID, summary.OutputEpoch, binding.PublicationGeneration, string(encodedBinding)).Exec(ctx); err != nil {
			return fmt.Errorf("admit explicit publication source cleanup retry: insert grant: %w", err)
		}
		for _, item := range binding.Items {
			if _, err := tx.NewRaw(`INSERT INTO source_cleanup_explicit_retry_item(retry_id,original_operation_id,item_id,owner_uuid) VALUES (?,?,?,?)`, grantID, originalOperationID, item.ItemID, item.OwnerUUID).Exec(ctx); err != nil {
				return fmt.Errorf("admit explicit publication source cleanup retry: bind retained item: %w", err)
			}
		}
		result.OperationID = recoveryOperationID
		return nil
	})
	if err != nil {
		return PublicationSourceCleanupExplicitRetryResult{}, err
	}
	return result, nil
}

func readPublicationSourceCleanupRetryBinding(ctx context.Context, tx bun.IDB, guard PublicationSourceCleanupDeliveryGuard, claim PublicationExecutionClaim, summary publicationSourceCleanupOperationSnapshot) (publicationSourceCleanupRetryBinding, error) {
	binding := publicationSourceCleanupRetryBinding{
		Version: 1, OriginalOperationID: guard.OperationID, OriginalApplicationAttempt: guard.ApplicationAttempt,
		OriginalRiverJobID: guard.RiverJobID, OriginalRiverAttempt: claim.RiverAttempt, OriginalClaimUUID: claim.ClaimUUID,
		OriginalBootUUID: claim.BootUUID, OriginalDeliveryToken: claim.DeliveryToken, OriginalClaimState: claim.State,
		Epoch: guard.Epoch, PositionID: guard.PositionID,
	}
	// The owner-returned operation/claim tuple is retained authority. River may
	// garbage-collect the old job without invalidating that tuple.
	binding.OriginalRiverKind = (publicationSourceCleanupJobArgs{}).Kind()
	if err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=? AND epoch=?`, guard.PositionID, guard.Epoch).Scan(ctx, &binding.PublicationGeneration); err != nil {
		return binding, err
	}
	var items []SourceCleanupItem
	if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").Scan(ctx); err != nil {
		return binding, err
	}
	var owners []SourceCleanupOwner
	if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").Scan(ctx); err != nil {
		return binding, err
	}
	if len(items) == 0 || len(items) != len(owners) {
		return binding, fmt.Errorf("retained item and owner set changed after validation")
	}
	binding.Items = make([]publicationSourceCleanupRetryItemBinding, len(items))
	for i, item := range items {
		owner := owners[i]
		if owner.ItemID != item.ID || item.BorrowerRevision == nil || *item.BorrowerRevision == uuid.Nil {
			return binding, fmt.Errorf("retained item-owner identity changed after validation")
		}
		var started, completed *string
		if item.MutationStartedAt != nil {
			value := item.MutationStartedAt.UTC().Format(time.RFC3339Nano)
			started = &value
		}
		if item.MutationCompletedAt != nil {
			value := item.MutationCompletedAt.UTC().Format(time.RFC3339Nano)
			completed = &value
		}
		binding.Items[i] = publicationSourceCleanupRetryItemBinding{
			ItemID: item.ID, State: item.State, SafeError: item.SafeError, SourceSnapshot: append(json.RawMessage(nil), item.SourceSnapshot...),
			BorrowerRevision: *item.BorrowerRevision, MutationClaimUUID: item.MutationClaimUUID,
			MutationStartedAt: started, MutationCompletedAt: completed, OwnerUUID: owner.OwnerUUID,
			SourceRootID: owner.SourceRootID, SourceLocationID: owner.SourceLocationID, WorkID: owner.WorkID,
		}
	}
	return binding, nil
}

var _ river.JobArgs = publicationSourceCleanupRetryJobArgs{}
