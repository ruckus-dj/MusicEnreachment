package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupDeliveryGuard identifies one retained cleanup
// delivery. Acquiring this identity claim is not authorization to start source
// removal or to unlink anything.
type PublicationSourceCleanupDeliveryGuard struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	RiverJobID         int64
	RiverAttempt       int
	ClaimUUID          uuid.UUID
	BootUUID           uuid.UUID
	DeliveryToken      string
	Epoch              uuid.UUID
	PositionID         uuid.UUID
}

// ClaimPublicationSourceCleanupDelivery retains the identity of one admitted
// cleanup delivery. The legacy "executing" state here records delivery
// ownership only; this method performs no cleanup execution or state transition.
func (repository *SetupManagerRepository) ClaimPublicationSourceCleanupDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard) (PublicationDeliveryClaimOutcome, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return PublicationDeliveryBlocked, errors.New("invalid publication source cleanup delivery guard")
	}
	outcome := PublicationDeliveryBlocked
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: acquire output admission: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: lock source mutations: %w", err)
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication source cleanup delivery: read namespace epoch: %w", err)
		}
		if epoch != guard.Epoch {
			return nil
		}
		op := new(Operation)
		if err := tx.NewSelect().Model(op).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication source cleanup delivery: lock operation: %w", err)
		}
		if op.Kind != "cleanup_publication_sources_v1" || op.State != "queued" || op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID {
			return nil
		}
		var summary publicationSourceCleanupOperationSnapshot
		if err := decodeStrictJSON(op.InputSnapshot, &summary); err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: decode operation snapshot: %w", err)
		}
		if summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch {
			return nil
		}
		var items []SourceCleanupItem
		if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim publication source cleanup delivery: lock items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		for _, item := range items {
			if item.State != "queued" || item.OperationID != guard.OperationID || item.PositionID != guard.PositionID || item.Epoch != guard.Epoch {
				return nil
			}
		}
		var owners []SourceCleanupOwner
		if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim publication source cleanup delivery: lock owners: %w", err)
		}
		if len(owners) != len(items) {
			return nil
		}
		itemIDs := make(map[uuid.UUID]struct{}, len(items))
		for _, item := range items {
			itemIDs[item.ID] = struct{}{}
		}
		for _, owner := range owners {
			if _, ok := itemIDs[owner.ItemID]; !ok || owner.OperationID != guard.OperationID || owner.ApplicationAttempt != guard.ApplicationAttempt || owner.RiverJobID != guard.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID == uuid.Nil || owner.SourceLocationID == uuid.Nil || owner.WorkID == uuid.Nil {
				return nil
			}
		}
		var riverKind, riverState string
		var riverAttempt int
		var riverArgs []byte
		if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &riverArgs); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim publication source cleanup delivery: lock River job: %w", err)
		}
		var args struct {
			OperationID uuid.UUID `json:"operation_id"`
		}
		if err := decodeStrictJSON(riverArgs, &args); err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: decode River args: %w", err)
		}
		if riverKind != "cleanup_publication_sources_v1" || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
			return nil
		}
		var setting string
		err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &setting)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: read publication_remove_sources: %w", err)
		}
		enabled, err := strconv.ParseBool(setting)
		if err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: parse publication_remove_sources: %w", err)
		}
		if !enabled {
			return nil
		}
		var existing []PublicationExecutionClaim
		if err := tx.NewSelect().Model(&existing).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim publication source cleanup delivery: read claims: %w", err)
		}
		if len(existing) != 0 {
			if len(existing) == 1 && publicationSourceCleanupClaimMatches(existing[0], guard) && existing[0].State == "executing" {
				outcome = PublicationDeliveryAlreadyOwned
			}
			return nil
		}
		claim := PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt, ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "executing"}
		if _, err := tx.NewInsert().Model(&claim).Exec(ctx); err != nil {
			return fmt.Errorf("claim publication source cleanup delivery: insert claim: %w", err)
		}
		outcome = PublicationDeliveryAcquired
		return nil
	})
	if err != nil {
		return PublicationDeliveryBlocked, err
	}
	return outcome, nil
}

func publicationSourceCleanupClaimMatches(claim PublicationExecutionClaim, guard PublicationSourceCleanupDeliveryGuard) bool {
	return claim.OperationID == guard.OperationID && claim.ApplicationAttempt == guard.ApplicationAttempt && claim.RiverJobID == guard.RiverJobID && claim.RiverAttempt == guard.RiverAttempt && claim.ClaimUUID == guard.ClaimUUID && claim.BootUUID == guard.BootUUID && claim.DeliveryToken == guard.DeliveryToken && claim.Epoch == guard.Epoch
}
