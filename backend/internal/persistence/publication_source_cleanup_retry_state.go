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
	OperationID           uuid.UUID
	Kind                  string
	State                 string
	Stage                 string
	PositionID            uuid.UUID
	OutputEpoch           uuid.UUID
	SnapshotValid         bool
	CurrentGrant          bool
	RecoveryGrantValid    bool
	RecoveryOperationID   uuid.UUID
	RecoveryKind          string
	RecoveryState         string
	RecoveryStage         string
	RecoverySnapshotValid bool
	RecoveryClaimState    string
	RecoveryFrontier      bool
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
		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", operationID).Scan(ctx); err != nil {
			return err
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
			state.CurrentGrant = true // malformed/ambiguous is never initial-admission authority
			return nil
		}
		if len(grants) == 1 {
			grant := grants[0]
			state.CurrentGrant = true
			state.RecoveryOperationID = grant.RecoveryOperationID
			state.RecoveryGrantValid = grant.ID != uuid.Nil && grant.OriginalOperationID == operationID && grant.RecoveryOperationID != uuid.Nil && state.SnapshotValid && grant.PositionID == state.PositionID && grant.Epoch == state.OutputEpoch
			var recovery Operation
			if err := tx.NewSelect().Model(&recovery).Where("id=?", grant.RecoveryOperationID).Scan(ctx); err != nil {
				if err == sql.ErrNoRows {
					return nil
				}
				return fmt.Errorf("read current retry recovery operation: %w", err)
			}
			state.RecoveryKind, state.RecoveryState, state.RecoveryStage = recovery.Kind, recovery.State, recovery.Stage
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
				state.RecoveryClaimState = claims[0].State
			}
		}
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_recovery_state WHERE original_operation_id=?)`, operationID).Scan(ctx, &state.RecoveryFrontier); err != nil {
			return fmt.Errorf("read explicit retry recovery frontier: %w", err)
		}
		return nil
	})
	if err != nil {
		return PublicationSourceCleanupExplicitRetryState{}, fmt.Errorf("read explicit publication source cleanup retry state: %w", err)
	}
	return state, nil
}
