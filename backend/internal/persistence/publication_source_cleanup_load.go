package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupDeliveryInput is a read-only snapshot for a worker.
// RemovalEnabled is advisory only; every mutating operation rechecks it.
type PublicationSourceCleanupDeliveryInput struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	Epoch              uuid.UUID
	PositionID         uuid.UUID
	ItemIDs            []uuid.UUID
	RemovalEnabled     bool
}

// LoadPublicationSourceCleanupDelivery reads and validates the operation's
// immutable summary and complete item/owner set without taking authority locks.
func (repository *SetupManagerRepository) LoadPublicationSourceCleanupDelivery(ctx context.Context, operationID uuid.UUID) (PublicationSourceCleanupDeliveryInput, error) {
	input := PublicationSourceCleanupDeliveryInput{OperationID: operationID}
	if operationID == uuid.Nil {
		return input, fmt.Errorf("load publication source cleanup delivery: operation is required")
	}
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var operation Operation
		if err := tx.NewSelect().Model(&operation).Where("id=?", operationID).Scan(ctx); err != nil {
			return fmt.Errorf("read operation: %w", err)
		}
		if operation.Kind != "cleanup_publication_sources_v1" || (operation.State != "queued" && operation.State != "running") || operation.Attempt <= 0 || operation.RiverJobID == nil || *operation.RiverJobID <= 0 {
			return fmt.Errorf("operation is not a valid publication source cleanup delivery")
		}
		wantItemState := "queued"
		if operation.State == "running" {
			wantItemState = "claimed"
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(operation.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("decode operation summary: %w", err)
		}
		if summary.Version != 1 || summary.PositionID == uuid.Nil || summary.OutputEpoch == uuid.Nil {
			return fmt.Errorf("operation summary is invalid")
		}

		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", operationID).Order("id ASC").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read cleanup items: %w", err)
		}
		if len(items) == 0 {
			return fmt.Errorf("cleanup item set is empty")
		}
		itemIDs := make([]uuid.UUID, 0, len(items))
		for _, item := range items {
			if item.ID == uuid.Nil || item.OperationID != operationID || item.PositionID != summary.PositionID || item.Epoch != summary.OutputEpoch || item.State != wantItemState {
				return fmt.Errorf("cleanup item set is incomplete or inconsistent")
			}
			itemIDs = append(itemIDs, item.ID)
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", operationID).Order("item_id ASC").Scan(ctx); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read cleanup item owners: %w", err)
		}
		if len(owners) != len(items) {
			return fmt.Errorf("cleanup item owner set is incomplete")
		}
		for i, owner := range owners {
			if owner.ItemID != itemIDs[i] || owner.OperationID != operationID || owner.ApplicationAttempt != operation.Attempt || owner.RiverJobID != *operation.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID == uuid.Nil || owner.SourceLocationID == uuid.Nil || owner.WorkID == uuid.Nil {
				return fmt.Errorf("cleanup item owner set is inconsistent")
			}
		}
		sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i].String() < itemIDs[j].String() })
		input.ApplicationAttempt = operation.Attempt
		input.Epoch = summary.OutputEpoch
		input.PositionID = summary.PositionID
		input.ItemIDs = itemIDs

		var setting string
		err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &setting)
		if err == sql.ErrNoRows {
			input.RemovalEnabled = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("read publication removal setting: %w", err)
		}
		enabled, err := strconv.ParseBool(setting)
		if err != nil {
			return fmt.Errorf("parse publication removal setting: %w", err)
		}
		input.RemovalEnabled = enabled
		return nil
	})
	if err != nil {
		return PublicationSourceCleanupDeliveryInput{}, fmt.Errorf("load publication source cleanup delivery: %w", err)
	}
	return input, nil
}
