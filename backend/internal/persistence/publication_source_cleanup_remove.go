package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type PublicationSourceCleanupRemovalRequest struct {
	ConfiguredRootPath string
	RelativePath       string
	SizeBytes          int64
	MTime              time.Time
}

type PublicationSourceCleanupRemovalOutcome string

const (
	PublicationSourceCleanupRemovalBlocked          PublicationSourceCleanupRemovalOutcome = "blocked"
	PublicationSourceCleanupRemovalCompleted        PublicationSourceCleanupRemovalOutcome = "completed"
	PublicationSourceCleanupRemovalAlreadyCompleted PublicationSourceCleanupRemovalOutcome = "already_completed"
)

// RemovePublicationSourceCleanupItem performs the durable mutation-start
// fence, invokes remove synchronously while the second validation transaction
// is held, and records completion only after successful removal. The callback
// must never be deferred to a background mutation.
func (repository *SetupManagerRepository) RemovePublicationSourceCleanupItem(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, itemID uuid.UUID, remove func(context.Context, PublicationSourceCleanupRemovalRequest) error) (PublicationSourceCleanupRemovalOutcome, error) {
	blocked := PublicationSourceCleanupRemovalBlocked
	if itemID == uuid.Nil || remove == nil {
		return blocked, fmt.Errorf("remove publication source cleanup item: item and remover are required")
	}
	var start time.Time
	var existingDone bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, guard, publicationSourceCleanupRemovalPhase)
		if err != nil || !valid {
			return err
		}
		var item SourceCleanupItem
		if err := tx.NewSelect().Model(&item).Where("id=? AND operation_id=?", itemID, guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("remove publication source cleanup item: read item: %w", err)
		}
		if item.State != "claimed" {
			return nil
		}
		if item.MutationClaimUUID != nil {
			if *item.MutationClaimUUID != guard.ClaimUUID || item.MutationStartedAt == nil || item.MutationStartedAt.IsZero() || item.MutationCompletedAt != nil && item.MutationCompletedAt.Before(*item.MutationStartedAt) {
				return nil
			}
			if item.MutationCompletedAt != nil {
				var frozen publicationSourceCleanupSnapshot
				if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
					return fmt.Errorf("remove publication source cleanup item: decode completed source: %w", err)
				}
				if err := recordSourceCleanupCompletion(ctx, tx, frozen, "native_removed"); err != nil {
					return err
				}
				existingDone = true
				return nil
			}
			return nil
		}
		if item.MutationStartedAt != nil || item.MutationCompletedAt != nil {
			return nil
		}
		if err := tx.NewRaw(`UPDATE source_cleanup_item SET mutation_claim_uuid=?,mutation_started_at=clock_timestamp()
			WHERE id=? AND operation_id=? AND state='claimed' AND mutation_claim_uuid IS NULL AND mutation_started_at IS NULL AND mutation_completed_at IS NULL
			RETURNING mutation_started_at`, guard.ClaimUUID, itemID, guard.OperationID).Scan(ctx, &start); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("remove publication source cleanup item: record mutation start: %w", err)
		}
		return nil
	})
	if err != nil {
		return blocked, err
	}
	if existingDone {
		return PublicationSourceCleanupRemovalAlreadyCompleted, nil
	}
	if start.IsZero() {
		return blocked, nil
	}

	var request PublicationSourceCleanupRemovalRequest
	completed := false
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		valid, err := validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, guard, publicationSourceCleanupRemovalPhase)
		if err != nil || !valid {
			return err
		}
		var item SourceCleanupItem
		if err := tx.NewSelect().Model(&item).Where("id=? AND operation_id=?", itemID, guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return fmt.Errorf("remove publication source cleanup item: reread item: %w", err)
		}
		if item.State != "claimed" || item.MutationClaimUUID == nil || *item.MutationClaimUUID != guard.ClaimUUID || item.MutationStartedAt == nil || !item.MutationStartedAt.Equal(start) || item.MutationCompletedAt != nil {
			return nil
		}
		var frozen publicationSourceCleanupSnapshot
		if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
			return fmt.Errorf("remove publication source cleanup item: decode frozen source: %w", err)
		}
		mtime, err := time.Parse(time.RFC3339Nano, frozen.Mtime)
		if err != nil || frozen.Version != 1 || frozen.ConfiguredPath == "" || frozen.RelativePath == "" || frozen.SizeBytes < 0 {
			return nil
		}
		request = PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: frozen.ConfiguredPath, RelativePath: frozen.RelativePath, SizeBytes: frozen.SizeBytes, MTime: mtime}
		if err := remove(ctx, request); err != nil {
			return fmt.Errorf("remove publication source cleanup item: remove source: %w", err)
		}
		if err := recordSourceCleanupCompletion(ctx, tx, frozen, "native_removed"); err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE source_cleanup_item SET mutation_completed_at=clock_timestamp()
			WHERE id=? AND operation_id=? AND state='claimed' AND mutation_claim_uuid=? AND mutation_started_at=? AND mutation_completed_at IS NULL`, itemID, guard.OperationID, guard.ClaimUUID, start).Exec(ctx)
		if err != nil {
			return fmt.Errorf("remove publication source cleanup item: record mutation completion: %w", err)
		}
		if err := requireRowsAffected(result, 1, "complete publication source cleanup item"); err != nil {
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
	return PublicationSourceCleanupRemovalCompleted, nil
}
