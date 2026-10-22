package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type publicationCleanupWarningRow struct {
	OperationID         uuid.UUID       `bun:"operation_id,type:uuid"`
	SafeError           string          `bun:"safe_error"`
	OperationState      string          `bun:"operation_state"`
	Snapshot            json.RawMessage `bun:"source_snapshot,type:jsonb"`
	OperationInput      json.RawMessage `bun:"operation_input,type:jsonb"`
	ItemID              uuid.UUID       `bun:"item_id,type:uuid"`
	ItemPositionID      uuid.UUID       `bun:"item_position_id,type:uuid"`
	ItemEpoch           uuid.UUID       `bun:"item_epoch,type:uuid"`
	ItemGeneration      uuid.UUID       `bun:"item_generation,type:uuid"`
	MutationClaim       *uuid.UUID      `bun:"mutation_claim_uuid,type:uuid"`
	Attempt             int             `bun:"application_attempt"`
	RiverJobID          *int64          `bun:"river_job_id"`
	OwnerUUID           uuid.UUID       `bun:"owner_uuid,type:uuid"`
	OwnerRootID         uuid.UUID       `bun:"owner_root_id,type:uuid"`
	OwnerLocation       uuid.UUID       `bun:"owner_location_id,type:uuid"`
	OwnerWork           uuid.UUID       `bun:"owner_work_id,type:uuid"`
	ClaimAttempt        int             `bun:"claim_attempt"`
	ClaimJobID          int64           `bun:"claim_job_id"`
	ClaimRiverAttempt   int             `bun:"claim_river_attempt"`
	ClaimUUID           uuid.UUID       `bun:"claim_uuid,type:uuid"`
	BootUUID            uuid.UUID       `bun:"boot_uuid,type:uuid"`
	DeliveryToken       string          `bun:"delivery_token"`
	ClaimState          string          `bun:"claim_state"`
	InitiatorGeneration uuid.UUID       `bun:"initiator_generation,type:uuid"`
	InitiatorEpoch      uuid.UUID       `bun:"initiator_epoch,type:uuid"`
	ViewGeneration      uuid.UUID       `bun:"view_generation,type:uuid"`
	ViewEpoch           uuid.UUID       `bun:"view_epoch,type:uuid"`
	ValidatedAt         sql.NullTime    `bun:"validated_at"`
}

// readLibraryPublicationSourceCleanupWarningsTx projects only failed cleanup
// deliveries with retained owner, claim, frozen snapshot, and current witnesses.
func readLibraryPublicationSourceCleanupWarningsTx(ctx context.Context, tx bun.IDB, positionID, generation, epoch uuid.UUID) ([]LibraryPublicationSourceCleanupWarning, error) {
	var enabledValue string
	settingErr := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &enabledValue)
	enabled := false
	if settingErr != nil && settingErr != sql.ErrNoRows {
		return nil, fmt.Errorf("read publication source removal setting: %w", settingErr)
	}
	if settingErr == nil {
		var err error
		enabled, err = strconv.ParseBool(enabledValue)
		if err != nil {
			return nil, fmt.Errorf("parse publication source removal setting: %w", err)
		}
	}
	var rows []publicationCleanupWarningRow
	err := tx.NewRaw(`SELECT o.id AS operation_id, COALESCE(o.safe_error,'') AS safe_error, o.state AS operation_state,
		i.source_snapshot, o.input_snapshot AS operation_input, i.id AS item_id,
		i.position_id AS item_position_id, i.epoch AS item_epoch,
		i.publication_generation AS item_generation, i.mutation_claim_uuid,
		own.application_attempt, own.river_job_id, own.owner_uuid,
		own.source_root_id AS owner_root_id, own.source_location_id AS owner_location_id, own.work_id AS owner_work_id,
		c.application_attempt AS claim_attempt, c.river_job_id AS claim_job_id, c.river_attempt AS claim_river_attempt, c.claim_uuid,
		c.boot_uuid, c.delivery_token, c.state AS claim_state,
		ip.generation AS initiator_generation, ip.epoch AS initiator_epoch,
		vp.generation AS view_generation, vp.epoch AS view_epoch, vp.validated_at
		FROM source_cleanup_item i
		JOIN source_cleanup_owner own ON own.item_id=i.id AND own.operation_id=i.operation_id
		JOIN operation o ON o.id=i.operation_id
		JOIN publication_execution_claim c ON c.operation_id=o.id
		JOIN publication_namespace_state ns ON ns.singleton=true
		JOIN track_publication ip ON ip.position_id=i.position_id
		JOIN track_publication vp ON vp.position_id=?
		WHERE i.state='recovery_required' AND o.kind='cleanup_publication_sources_v1'
		AND o.state='failed' AND o.stage='cleanup_recovery_required'
		AND c.state='recovery_required' AND c.application_attempt=o.attempt AND c.river_job_id=o.river_job_id
		AND own.application_attempt=o.attempt AND own.river_job_id=o.river_job_id
		AND i.epoch=ns.output_epoch AND i.epoch=ip.epoch AND i.publication_generation=ip.generation
		AND vp.generation=? AND vp.epoch=? AND vp.validated_at IS NOT NULL
		ORDER BY o.id, i.id`, positionID, generation, epoch).Scan(ctx, &rows)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query retained failed source cleanup owners: %w", err)
	}
	byOriginal := make(map[uuid.UUID]LibraryPublicationSourceCleanupWarning)
	for _, row := range rows {
		if row.OperationID == uuid.Nil || row.ItemID == uuid.Nil || strings.TrimSpace(row.SafeError) == "" ||
			row.ItemPositionID == uuid.Nil || row.ItemEpoch != epoch ||
			row.ItemGeneration == uuid.Nil || row.InitiatorGeneration != row.ItemGeneration || row.InitiatorEpoch != row.ItemEpoch ||
			row.ViewGeneration != generation || row.ViewEpoch != epoch || !row.ValidatedAt.Valid ||
			row.Attempt <= 0 || row.RiverJobID == nil || *row.RiverJobID <= 0 || row.ClaimAttempt != row.Attempt || row.ClaimJobID != *row.RiverJobID ||
			row.ClaimRiverAttempt <= 0 || row.ClaimUUID == uuid.Nil || row.OwnerUUID == uuid.Nil || row.BootUUID == uuid.Nil || strings.TrimSpace(row.DeliveryToken) == "" ||
			row.MutationClaim == nil || *row.MutationClaim != row.ClaimUUID {
			continue
		}
		var original publicationSourceCleanupOperationSnapshot
		if decodeStrictJSON(row.OperationInput, &original) != nil || original.Version != 1 || original.PositionID != row.ItemPositionID || original.OutputEpoch != row.ItemEpoch {
			continue
		}
		var frozen publicationSourceCleanupSnapshot
		if decodeStrictJSON(row.Snapshot, &frozen) != nil || frozen.Version != 1 || frozen.WorkID != row.OwnerWork || frozen.SourceRootID != row.OwnerRootID || frozen.SourceLocationID != row.OwnerLocation || frozen.MediaVariantID == uuid.Nil || strings.TrimSpace(frozen.RelativePath) == "" || len(frozen.SourceLinks) == 0 {
			continue
		}
		linksMatch := true
		for _, link := range frozen.SourceLinks {
			if link.ID == uuid.Nil || link.TrackID == uuid.Nil || link.MediaVariantID != frozen.MediaVariantID {
				linksMatch = false
				break
			}
		}
		if !linksMatch {
			continue
		}
		matched := false
		for _, borrower := range frozen.Borrowers {
			if borrower.PositionID == positionID && borrower.Generation == generation && borrower.Epoch == epoch && borrower.ValidatedAt.Equal(row.ValidatedAt.Time) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		byOriginal[row.OperationID] = LibraryPublicationSourceCleanupWarning{OriginalOperationID: row.OperationID, SafeError: row.SafeError}
	}
	warnings := make([]LibraryPublicationSourceCleanupWarning, 0, len(byOriginal))
	ids := make([]uuid.UUID, 0, len(byOriginal))
	for id := range byOriginal {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		warning := byOriginal[id]
		var retry PublicationSourceCleanupExplicitRetryState
		if err := readPublicationSourceCleanupExplicitRetryStateTx(ctx, tx, id, &retry); err != nil {
			return nil, fmt.Errorf("read retained cleanup retry for %s: %w", id, err)
		}
		if retry.CurrentGrant && retry.RecoveryGrantValid && retry.RecoverySnapshotValid && retry.RecoveryKind == "retry_publication_sources_v1" &&
			(retry.RecoveryState == "queued" || retry.RecoveryState == "running") && retry.RecoveryAttempt > 0 && retry.RecoveryRiverJobID != nil && *retry.RecoveryRiverJobID > 0 {
			recoveryID := retry.RecoveryOperationID
			warning.ActiveRetryOperationID = &recoveryID
		}
		warning.CanRetry = enabled && PublicationSourceCleanupRetryRouteFor(retry) != PublicationSourceCleanupRetryBlocked
		warnings = append(warnings, warning)
	}
	return warnings, nil
}
