package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupExplicitRetryDeliveryInput is advisory metadata for
// one retry delivery. It deliberately contains no credential from the original
// cleanup delivery; only the retry claim can establish execution authority.
type PublicationSourceCleanupExplicitRetryDeliveryInput struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	Epoch              uuid.UUID
	PositionID         uuid.UUID
	ItemIDs            []uuid.UUID
}

// LoadPublicationSourceCleanupExplicitRetryDelivery reads a consistent view of
// the retry operation and the exact item references retained by its grant.
func (repository *SetupManagerRepository) LoadPublicationSourceCleanupExplicitRetryDelivery(ctx context.Context, operationID uuid.UUID) (PublicationSourceCleanupExplicitRetryDeliveryInput, error) {
	input := PublicationSourceCleanupExplicitRetryDeliveryInput{OperationID: operationID}
	if operationID == uuid.Nil {
		return input, fmt.Errorf("load explicit publication source cleanup retry: operation is required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", operationID).Scan(ctx); err != nil {
			return fmt.Errorf("read retry operation: %w", err)
		}
		var snapshot publicationSourceCleanupRetryOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &snapshot); err != nil {
			return fmt.Errorf("decode retry operation snapshot: %w", err)
		}
		if operation.Kind != "retry_publication_sources_v1" || operation.State != "queued" || operation.Stage != "queued" || operation.Attempt <= 0 || operation.RiverJobID == nil || *operation.RiverJobID <= 0 || snapshot.Version != 1 || snapshot.PositionID == uuid.Nil || snapshot.OutputEpoch == uuid.Nil || snapshot.OriginalOperationID == uuid.Nil || snapshot.OriginalClaimUUID == uuid.Nil {
			return fmt.Errorf("retry operation identity or snapshot is invalid")
		}
		var grants []SourceCleanupExplicitRetry
		if err := tx.NewSelect().Model(&grants).Where("recovery_operation_id=?", operationID).Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read retry grant: %w", err)
		}
		if len(grants) != 1 {
			return fmt.Errorf("retry grant is missing or ambiguous")
		}
		grant := grants[0]
		if grant.ID == uuid.Nil || grant.RecoveryOperationID != operationID || grant.OriginalOperationID != snapshot.OriginalOperationID || grant.OriginalClaimUUID != snapshot.OriginalClaimUUID || grant.PositionID != snapshot.PositionID || grant.Epoch != snapshot.OutputEpoch || grant.OriginalApplicationAttempt <= 0 || grant.OriginalRiverJobID <= 0 {
			return fmt.Errorf("retry grant does not match operation snapshot")
		}
		var references []SourceCleanupExplicitRetryItem
		if err := tx.NewSelect().Model(&references).Where("retry_id=? AND original_operation_id=?", grant.ID, grant.OriginalOperationID).Order("item_id ASC").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read retained retry item references: %w", err)
		}
		if len(references) == 0 {
			return fmt.Errorf("retry item reference set is empty")
		}
		itemIDs := make([]uuid.UUID, 0, len(references))
		for _, reference := range references {
			if reference.RetryID != grant.ID || reference.OriginalOperationID != grant.OriginalOperationID || reference.ItemID == uuid.Nil || reference.OwnerUUID == uuid.Nil {
				return fmt.Errorf("retry item reference set is malformed")
			}
			itemIDs = append(itemIDs, reference.ItemID)
		}
		sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i].String() < itemIDs[j].String() })
		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", grant.OriginalOperationID).Where("id IN (?)", bun.List(itemIDs)).Order("id ASC").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read retained cleanup items: %w", err)
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", grant.OriginalOperationID).Where("item_id IN (?)", bun.List(itemIDs)).Order("item_id ASC").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read retained cleanup item owners: %w", err)
		}
		if len(items) != len(itemIDs) || len(owners) != len(itemIDs) {
			return fmt.Errorf("retained retry item set is incomplete")
		}
		for i := range itemIDs {
			item, owner, reference := items[i], owners[i], references[i]
			if item.ID != itemIDs[i] || item.OperationID != grant.OriginalOperationID || item.PositionID != snapshot.PositionID || item.Epoch != snapshot.OutputEpoch || (item.State != "claimed" && item.State != "recovery_required") || owner.ItemID != item.ID || owner.OperationID != grant.OriginalOperationID || owner.OwnerUUID != reference.OwnerUUID {
				return fmt.Errorf("retained retry item set is inconsistent")
			}
		}
		input.ApplicationAttempt = operation.Attempt
		input.Epoch = snapshot.OutputEpoch
		input.PositionID = snapshot.PositionID
		input.ItemIDs = itemIDs
		return nil
	})
	if err != nil {
		return PublicationSourceCleanupExplicitRetryDeliveryInput{}, fmt.Errorf("load explicit publication source cleanup retry: %w", err)
	}
	return input, nil
}
