package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupExplicitRetryState is advisory routing metadata for
// an explicit user retry. It carries no execution credentials or authority.
type PublicationSourceCleanupExplicitRetryState struct {
	OperationID                   uuid.UUID
	Kind                          string
	State                         string
	Stage                         string
	PositionID                    uuid.UUID
	OutputEpoch                   uuid.UUID
	SnapshotValid                 bool
	CurrentGrant                  bool
	RecoveryGrantValid            bool
	RecoveryOperationID           uuid.UUID
	RecoveryKind                  string
	RecoveryState                 string
	RecoveryStage                 string
	RecoverySnapshotValid         bool
	RecoveryClaimState            string
	RecoveryClaimValid            bool
	RecoveryFrontier              bool
	RecoveryFrontierValid         bool
	RecoveryGrantFrontierRevision *uuid.UUID
	RecoveryAttempt               int
	RecoveryRiverJobID            *int64
}

// ReadPublicationSourceCleanupExplicitRetryState reads a coherent advisory
// view used only to choose between initial admission and replacement. The
// admission/replacement methods recapture and validate all authoritative facts.
func (repository *SetupManagerRepository) ReadPublicationSourceCleanupExplicitRetryState(ctx context.Context, operationID uuid.UUID) (PublicationSourceCleanupExplicitRetryState, error) {
	state := PublicationSourceCleanupExplicitRetryState{OperationID: operationID}
	if operationID == uuid.Nil {
		return state, fmt.Errorf("read explicit publication source cleanup retry state: operation is required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationSourceCleanupExplicitRetryStateTx(ctx, tx, operationID, &state)
	})
	if err != nil {
		return PublicationSourceCleanupExplicitRetryState{}, fmt.Errorf("read explicit publication source cleanup retry state: %w", err)
	}
	return state, nil
}

// readPublicationSourceCleanupExplicitRetryStateTx lets read-only projections
// consume retry routing facts in their existing transaction.
func readPublicationSourceCleanupExplicitRetryStateTx(ctx context.Context, tx bun.IDB, operationID uuid.UUID, state *PublicationSourceCleanupExplicitRetryState) error {
	if operationID == uuid.Nil || state == nil {
		return fmt.Errorf("read explicit publication source cleanup retry state: operation and destination are required")
	}
	var operation Operation
	if err := tx.NewSelect().Model(&operation).Where("id=?", operationID).Scan(ctx); err != nil {
		return fmt.Errorf("read original cleanup operation: %w", err)
	}
	state.Kind, state.State, state.Stage = operation.Kind, operation.State, operation.Stage
	var summary publicationSourceCleanupOperationSnapshot
	if decodeStrictJSON(operation.InputSnapshot, &summary) == nil && summary.Version == 1 && summary.PositionID != uuid.Nil && summary.OutputEpoch != uuid.Nil {
		state.SnapshotValid = true
		state.PositionID, state.OutputEpoch = summary.PositionID, summary.OutputEpoch
	}

	var grants []SourceCleanupExplicitRetry
	if err := tx.NewSelect().Model(&grants).Where("original_operation_id=?", operationID).Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read current explicit retry grant: %w", err)
	}
	if len(grants) > 1 {
		state.CurrentGrant = true
		return nil
	}
	if len(grants) == 1 {
		grant := grants[0]
		state.CurrentGrant = true
		state.RecoveryOperationID = grant.RecoveryOperationID
		state.RecoveryGrantFrontierRevision = grant.FrontierRevision
		state.RecoveryGrantValid = grant.ID != uuid.Nil && grant.OriginalOperationID == operationID && grant.RecoveryOperationID != uuid.Nil && state.SnapshotValid && grant.PositionID == state.PositionID && grant.Epoch == state.OutputEpoch && grant.OriginalApplicationAttempt > 0 && grant.OriginalRiverJobID > 0 && grant.OriginalClaimUUID != uuid.Nil
		var binding publicationSourceCleanupRetryBinding
		if decodeStrictJSON(grant.Binding, &binding) == nil && binding.Version == 1 && binding.OriginalOperationID == operationID && binding.OriginalApplicationAttempt == grant.OriginalApplicationAttempt && binding.OriginalRiverJobID == grant.OriginalRiverJobID && binding.OriginalClaimUUID == grant.OriginalClaimUUID && binding.PositionID == grant.PositionID && binding.Epoch == grant.Epoch && binding.PublicationGeneration == grant.PublicationGeneration {
			state.RecoveryGrantValid = state.RecoveryGrantValid && true
		} else {
			state.RecoveryGrantValid = false
		}
		var recovery Operation
		if err := tx.NewSelect().Model(&recovery).Where("id=?", grant.RecoveryOperationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("read current retry recovery operation: %w", err)
		}
		state.RecoveryKind, state.RecoveryState, state.RecoveryStage = recovery.Kind, recovery.State, recovery.Stage
		state.RecoveryAttempt, state.RecoveryRiverJobID = recovery.Attempt, recovery.RiverJobID
		var retrySnapshot publicationSourceCleanupRetryOperationSnapshot
		if decodeStrictJSON(recovery.InputSnapshot, &retrySnapshot) == nil && retrySnapshot.Version == 1 && retrySnapshot.PositionID != uuid.Nil && retrySnapshot.OutputEpoch != uuid.Nil && retrySnapshot.OriginalOperationID == operationID {
			state.RecoverySnapshotValid = true
			state.RecoveryGrantValid = state.RecoveryGrantValid && retrySnapshot.PositionID == grant.PositionID && retrySnapshot.OutputEpoch == grant.Epoch && retrySnapshot.OriginalClaimUUID == grant.OriginalClaimUUID
		}
		var claims []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", recovery.ID).Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read retry recovery claim state: %w", err)
		}
		if len(claims) == 1 {
			claim := claims[0]
			state.RecoveryClaimState = claim.State
			state.RecoveryClaimValid = claim.ApplicationAttempt == recovery.Attempt && recovery.RiverJobID != nil && claim.RiverJobID == *recovery.RiverJobID && claim.RiverAttempt > 0 && claim.Epoch == grant.Epoch && claim.ClaimUUID != uuid.Nil && claim.BootUUID != uuid.Nil && claim.DeliveryToken != "" && grant.ConsumedApplicationAttempt != nil && *grant.ConsumedApplicationAttempt == claim.ApplicationAttempt && grant.ConsumedRiverJobID != nil && *grant.ConsumedRiverJobID == claim.RiverJobID && grant.ConsumedClaimUUID != nil && *grant.ConsumedClaimUUID == claim.ClaimUUID
		}
	}
	var frontiers []SourceCleanupRecoveryState
	if err := tx.NewSelect().Model(&frontiers).Where("original_operation_id=?", operationID).Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read explicit retry recovery frontier: %w", err)
	}
	state.RecoveryFrontier = len(frontiers) != 0
	if len(frontiers) == 1 {
		frontier := frontiers[0]
		var proof publicationSourceCleanupFrontierProof
		proofValid := decodeStrictJSON(frontier.Proof, &proof) == nil && proof.Version == 1 && proof.ReturnedRecoveryOperationID == frontier.ReturnedRecoveryOperationID && proof.ReturnedApplicationAttempt == frontier.ReturnedApplicationAttempt && proof.ReturnedRiverJobID == frontier.ReturnedRiverJobID && proof.ReturnedRiverAttempt == frontier.ReturnedRiverAttempt && proof.ReturnedClaimUUID == frontier.ReturnedClaimUUID && proof.ReturnedBootUUID == frontier.ReturnedBootUUID && proof.ReturnedDeliveryToken == frontier.ReturnedDeliveryToken
		// CURRENT records the retired predecessor, while the grant pins its
		// successor. The current successor's owner-return proof is checked above.
		state.RecoveryFrontierValid = frontier.Revision != uuid.Nil && state.RecoveryGrantFrontierRevision != nil && *state.RecoveryGrantFrontierRevision == frontier.Revision && frontier.ReturnedRecoveryOperationID != uuid.Nil && frontier.ReturnedRecoveryOperationID != state.RecoveryOperationID && frontier.ReturnedApplicationAttempt > 0 && frontier.ReturnedRiverJobID > 0 && frontier.ReturnedRiverAttempt > 0 && frontier.ReturnedClaimUUID != uuid.Nil && frontier.ReturnedBootUUID != uuid.Nil && frontier.ReturnedDeliveryToken != "" && proofValid
	}
	return nil
}

type PublicationSourceCleanupRetryRoute uint8

const (
	PublicationSourceCleanupRetryBlocked PublicationSourceCleanupRetryRoute = iota
	PublicationSourceCleanupRetryInitial
	PublicationSourceCleanupRetryReplacement
)

// PublicationSourceCleanupRetryRouteFor is the shared, advisory routing rule.
// Transactional admission still revalidates all durable authority.
func PublicationSourceCleanupRetryRouteFor(state PublicationSourceCleanupExplicitRetryState) PublicationSourceCleanupRetryRoute {
	if state.Kind != "cleanup_publication_sources_v1" || state.State != "failed" || state.Stage != "cleanup_recovery_required" || !state.SnapshotValid {
		return PublicationSourceCleanupRetryBlocked
	}
	if !state.CurrentGrant {
		if state.RecoveryFrontier {
			return PublicationSourceCleanupRetryBlocked
		}
		return PublicationSourceCleanupRetryInitial
	}
	if state.RecoveryOperationID == uuid.Nil || state.RecoveryKind != "retry_publication_sources_v1" || !state.RecoveryGrantValid || !state.RecoverySnapshotValid {
		return PublicationSourceCleanupRetryBlocked
	}
	if state.RecoveryFrontier {
		if !state.RecoveryFrontierValid || state.RecoveryGrantFrontierRevision == nil {
			return PublicationSourceCleanupRetryBlocked
		}
	} else if state.RecoveryGrantFrontierRevision != nil {
		return PublicationSourceCleanupRetryBlocked
	}
	if (state.RecoveryState == "queued" || state.RecoveryState == "running") && state.RecoveryAttempt > 0 && state.RecoveryRiverJobID != nil && *state.RecoveryRiverJobID > 0 {
		return PublicationSourceCleanupRetryBlocked
	}
	if state.RecoveryState == "failed" && state.RecoveryStage == "cleanup_retry_recovery_required" && state.RecoveryClaimState == "recovery_required" && state.RecoveryClaimValid {
		return PublicationSourceCleanupRetryReplacement
	}
	return PublicationSourceCleanupRetryBlocked
}
