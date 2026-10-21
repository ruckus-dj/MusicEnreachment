package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// StartPublicationSourceCleanupExecution atomically starts the exact retained
// delivery after revalidating its current inputs. It performs no filesystem
// mutation and grants no authority to skip per-item pre-unlink validation.
func (repository *SetupManagerRepository) StartPublicationSourceCleanupExecution(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard) (bool, error) {
	started := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		valid, err := validatePublicationSourceCleanupExecutionInputsTx(ctx, tx, guard)
		if err != nil || !valid {
			return err
		}
		var itemCount int64
		if err := tx.NewRaw(`SELECT count(*) FROM source_cleanup_item WHERE operation_id=? AND state='queued'`, guard.OperationID).Scan(ctx, &itemCount); err != nil {
			return fmt.Errorf("start publication source cleanup: count validated items: %w", err)
		}
		if itemCount == 0 {
			return fmt.Errorf("start publication source cleanup: validated items are missing")
		}
		operation, err := tx.NewRaw(`UPDATE operation SET state='running',stage='running',started_at=now(),updated_at=now()
			WHERE id=? AND kind='cleanup_publication_sources_v1' AND state='queued' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("start publication source cleanup: update operation: %w", err)
		}
		if err := requireRowsAffected(operation, 1, "start publication source cleanup operation"); err != nil {
			return err
		}
		items, err := tx.NewRaw(`UPDATE source_cleanup_item SET state='claimed' WHERE operation_id=? AND state='queued'`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("start publication source cleanup: claim items: %w", err)
		}
		if err := requireRowsAffected(items, itemCount, "start publication source cleanup items"); err != nil {
			return err
		}
		started = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return started, nil
}

// validatePublicationSourceCleanupExecutionInputsTx validates the retained
// delivery and frozen source evidence against current database facts. Callers
// must invoke it in the transaction that immediately starts execution; a result
// from an earlier transaction is not execution authority.
func validatePublicationSourceCleanupExecutionInputsTx(ctx context.Context, tx bun.Tx, guard PublicationSourceCleanupDeliveryGuard) (bool, error) {
	return validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx, tx, guard, publicationSourceCleanupQueuedPhase)
}

type publicationSourceCleanupExecutionPhase uint8

const (
	publicationSourceCleanupQueuedPhase publicationSourceCleanupExecutionPhase = iota
	publicationSourceCleanupRemovalPhase
)

func validatePublicationSourceCleanupExecutionInputsPhaseTx(ctx context.Context, tx bun.Tx, guard PublicationSourceCleanupDeliveryGuard, phase publicationSourceCleanupExecutionPhase) (bool, error) {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 || guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" || guard.Epoch == uuid.Nil || guard.PositionID == uuid.Nil {
		return false, nil
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: acquire output gate: %w", err)
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: lock source mutations: %w", err)
	}
	// Discover candidate identities before taking row locks. The values are only
	// used to establish deterministic lock order; every fact is read again below.
	var preliminaryItems []struct {
		ID             uuid.UUID       `bun:"id"`
		SourceSnapshot json.RawMessage `bun:"source_snapshot"`
	}
	if err := tx.NewRaw(`SELECT id,source_snapshot FROM source_cleanup_item WHERE operation_id=? ORDER BY id`, guard.OperationID).Scan(ctx, &preliminaryItems); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: discover items: %w", err)
	}
	var candidates []PublicationSourceCleanupCandidate
	for _, item := range preliminaryItems {
		var frozen publicationSourceCleanupSnapshot
		if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: decode frozen source snapshot: %w", err)
		}
		if frozen.Version != 1 {
			return false, nil
		}
		candidates = append(candidates, PublicationSourceCleanupCandidate{WorkID: frozen.WorkID, SourceRootID: frozen.SourceRootID, SourceLocationID: frozen.SourceLocationID, MediaVariantID: frozen.MediaVariantID})
	}
	initial := PublicationSourceCleanupAssessment{PositionID: guard.PositionID}
	if err := readPublicationSourceCleanupAssessmentTx(ctx, tx, guard.PositionID, &initial); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: discover current candidates: %w", err)
	}
	candidates = append(candidates, initial.Candidates...)
	if len(candidates) == 0 {
		return false, nil
	}
	if err := lockCleanupCandidateRows(ctx, tx, candidates); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: lock source identities: %w", err)
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: read namespace epoch: %w", err)
	}
	if epoch != guard.Epoch {
		return false, nil
	}
	if err := lockCleanupBorrowerPositions(ctx, tx, candidates); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: lock borrower positions: %w", err)
	}
	var operation Operation
	if err := tx.NewSelect().Model(&operation).Where("id = ?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: read operation: %w", err)
	}
	var summary publicationSourceCleanupOperationSnapshot
	if err := decodeStrictJSON(operation.InputSnapshot, &summary); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: decode operation snapshot: %w", err)
	}
	wantOperationState, wantItemState := "queued", "queued"
	if phase == publicationSourceCleanupRemovalPhase {
		wantOperationState, wantItemState = "running", "claimed"
	} else if phase != publicationSourceCleanupQueuedPhase {
		return false, nil
	}
	if operation.Kind != "cleanup_publication_sources_v1" || operation.State != wantOperationState || operation.Attempt != guard.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != guard.RiverJobID || summary.Version != 1 || summary.PositionID != guard.PositionID || summary.OutputEpoch != guard.Epoch {
		return false, nil
	}
	var setting string
	err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &setting)
	switch err {
	case nil:
		enabled, parseErr := strconv.ParseBool(setting)
		if parseErr != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: parse setting: %w", parseErr)
		}
		if !enabled {
			return false, nil
		}
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, fmt.Errorf("validate publication source cleanup inputs: read setting: %w", err)
	}
	var items []SourceCleanupItem
	if err := tx.NewSelect().Model(&items).Where("operation_id=?", guard.OperationID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("validate publication source cleanup inputs: lock items: %w", err)
	}
	if len(items) == 0 {
		return false, nil
	}
	var owners []SourceCleanupOwner
	if err := tx.NewSelect().Model(&owners).Where("operation_id=?", guard.OperationID).Order("item_id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("validate publication source cleanup inputs: lock owners: %w", err)
	}
	if len(owners) != len(items) {
		return false, nil
	}
	var riverKind, riverState string
	var riverAttempt int
	var args json.RawMessage
	if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &args); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: read River job: %w", err)
	}
	var jobArgs publicationSourceCleanupJobArgs
	if err := decodeStrictJSON(args, &jobArgs); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: decode River args: %w", err)
	}
	if riverKind != jobArgs.Kind() || riverState != "running" || riverAttempt != guard.RiverAttempt || jobArgs.OperationID != guard.OperationID || jobArgs.OperationID == uuid.Nil {
		return false, nil
	}
	var extraClaims []PublicationExecutionClaim
	if err := tx.NewSelect().Model(&extraClaims).Where("operation_id=?", guard.OperationID).Order("application_attempt ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("validate publication source cleanup inputs: lock execution claims: %w", err)
	}
	if len(extraClaims) != 1 {
		return false, nil
	}
	var claim PublicationExecutionClaim
	if err := tx.NewSelect().Model(&claim).Where("operation_id=? AND application_attempt=?", guard.OperationID, guard.ApplicationAttempt).For("UPDATE").Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate publication source cleanup inputs: read execution claim: %w", err)
	}
	if !publicationSourceCleanupClaimMatches(claim, guard) || claim.State != "executing" || extraClaims[0].ApplicationAttempt != guard.ApplicationAttempt {
		return false, nil
	}
	var assessment PublicationSourceCleanupAssessment
	assessment.PositionID = guard.PositionID
	if err := readPublicationSourceCleanupAssessmentTx(ctx, tx, guard.PositionID, &assessment); err != nil {
		return false, fmt.Errorf("validate publication source cleanup inputs: read current publication: %w", err)
	}
	if assessment.Epoch != guard.Epoch || assessment.Generation == uuid.Nil || assessment.ValidatedAt == nil || assessment.ValidatedAt.IsZero() {
		return false, nil
	}
	currentSet := make(map[string]PublicationSourceCleanupCandidate, len(assessment.Candidates))
	for _, c := range assessment.Candidates {
		key := cleanupIdentityKey(c)
		if _, duplicate := currentSet[key]; duplicate {
			return false, nil
		}
		currentSet[key] = c
	}
	seen := make(map[string]bool, len(items))
	ownerUUIDs := make(map[string]uuid.UUID, len(items))
	ownerItemIDs := make(map[string]uuid.UUID, len(items))
	for i, item := range items {
		frontierValid := item.MutationClaimUUID == nil && item.MutationStartedAt == nil && item.MutationCompletedAt == nil
		if phase == publicationSourceCleanupRemovalPhase {
			frontierValid = item.MutationClaimUUID == nil && item.MutationStartedAt == nil && item.MutationCompletedAt == nil ||
				item.MutationClaimUUID != nil && *item.MutationClaimUUID == guard.ClaimUUID && item.MutationStartedAt != nil && !item.MutationStartedAt.IsZero() && (item.MutationCompletedAt == nil || !item.MutationCompletedAt.Before(*item.MutationStartedAt))
		}
		if item.PositionID != guard.PositionID || item.Epoch != guard.Epoch || item.PublicationGeneration != assessment.Generation || item.State != wantItemState || !frontierValid || len(item.SourceSnapshot) == 0 || item.BorrowerRevision == nil || *item.BorrowerRevision == uuid.Nil {
			return false, nil
		}
		var frozen publicationSourceCleanupSnapshot
		if err := decodeStrictJSON(item.SourceSnapshot, &frozen); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: decode source snapshot: %w", err)
		}
		if _, err := time.Parse(time.RFC3339Nano, frozen.Mtime); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: parse frozen source mtime: %w", err)
		}
		key := cleanupSnapshotIdentityKey(frozen)
		candidate, exists := currentSet[key]
		if !exists || seen[key] {
			return false, nil
		}
		seen[key] = true
		owner := owners[i]
		if owner.ItemID != item.ID || owner.OperationID != guard.OperationID || owner.ApplicationAttempt != guard.ApplicationAttempt || owner.RiverJobID != guard.RiverJobID || owner.OwnerUUID == uuid.Nil || owner.SourceRootID != frozen.SourceRootID || owner.SourceLocationID != frozen.SourceLocationID || owner.WorkID != frozen.WorkID {
			return false, nil
		}
		ownerUUIDs[key] = owner.OwnerUUID
		ownerItemIDs[key] = owner.ItemID
		fresh, err := capturePublicationSourceCleanupSnapshot(ctx, tx, candidate)
		if err != nil {
			if err == errCleanupCandidateConflict || err == sql.ErrNoRows {
				return false, nil
			}
			return false, fmt.Errorf("validate publication source cleanup inputs: capture current evidence: %w", err)
		}
		if !cleanupSnapshotEqual(frozen, fresh) {
			return false, nil
		}
	}
	if len(seen) != len(currentSet) {
		return false, nil
	}
	if err := verifyCleanupCandidateFacts(ctx, tx, assessment.Candidates); err != nil {
		return false, nil
	}
	for _, candidate := range assessment.Candidates {
		for _, borrower := range candidate.Borrowers {
			if borrower.Generation == nil || *borrower.Generation == uuid.Nil || borrower.Epoch == nil || *borrower.Epoch != guard.Epoch || borrower.ValidatedAt == nil || borrower.ValidatedAt.IsZero() {
				return false, nil
			}
		}
		// Recheck structured facts rather than interpreting advisory blocker text.
		// Ownership is checked independently below, with an exact self exemption.
		var consistent bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_analysis_work w
			JOIN source_work_variant_identity identity ON identity.work_id=w.id
			JOIN source_location l ON l.id=w.current_location_id
			JOIN source_root r ON r.id=w.source_root_id
			WHERE w.id=? AND l.id=? AND r.id=? AND identity.media_variant_id=?
			AND l.media_variant_id=identity.media_variant_id AND l.source_root_id=r.id
			AND w.location_id=l.id AND w.relative_path=l.relative_path
			AND w.size_bytes=l.size_bytes AND date_trunc('microseconds',w.mtime)=date_trunc('microseconds',l.mtime)
			AND r.enabled AND r.status=? AND r.inventory_path=r.configured_path
			AND w.configured_path=r.configured_path AND w.inventory_path=r.inventory_path
		)`, candidate.WorkID, candidate.SourceLocationID, candidate.SourceRootID, candidate.MediaVariantID, SourceRootStatusAvailable).Scan(ctx, &consistent); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: verify current source facts: %w", err)
		}
		if !consistent {
			return false, nil
		}
	}
	// Publication holds always block. The exact already-validated owner tuple is
	// ignored below; all other cleanup owners remain protective.
	for _, candidate := range assessment.Candidates {
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_source_hold WHERE work_id=? OR source_location_id=?)`, candidate.WorkID, candidate.SourceLocationID).Scan(ctx, &held); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: read publication holds: %w", err)
		}
		if held {
			return false, nil
		}
		var activeConsumer bool
		if err := tx.NewRaw(`SELECT EXISTS(
			SELECT 1 FROM operation_source_work_hold h
			WHERE h.work_id=?
		)`, candidate.WorkID).Scan(ctx, &activeConsumer); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: read active source consumers: %w", err)
		}
		if activeConsumer {
			return false, nil
		}
		var foreign bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_owner o WHERE (o.work_id=? OR o.source_location_id=?) AND NOT (o.item_id=? AND o.operation_id=? AND o.application_attempt=? AND o.river_job_id=? AND o.owner_uuid=? AND o.source_root_id=? AND o.source_location_id=? AND o.work_id=?))`, candidate.WorkID, candidate.SourceLocationID, ownerItemIDs[cleanupIdentityKey(candidate)], guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, ownerUUIDs[cleanupIdentityKey(candidate)], candidate.SourceRootID, candidate.SourceLocationID, candidate.WorkID).Scan(ctx, &foreign); err != nil {
			return false, fmt.Errorf("validate publication source cleanup inputs: read competing owners: %w", err)
		}
		if foreign {
			return false, nil
		}
	}
	return true, nil
}

func cleanupIdentityKey(c PublicationSourceCleanupCandidate) string {
	return c.WorkID.String() + ":" + c.SourceRootID.String() + ":" + c.SourceLocationID.String() + ":" + c.MediaVariantID.String()
}
func cleanupSnapshotIdentityKey(s publicationSourceCleanupSnapshot) string {
	return s.WorkID.String() + ":" + s.SourceRootID.String() + ":" + s.SourceLocationID.String() + ":" + s.MediaVariantID.String()
}

func cleanupSnapshotEqual(a, b publicationSourceCleanupSnapshot) bool {
	if a.Version != b.Version || a.WorkID != b.WorkID || a.SourceRootID != b.SourceRootID || a.SourceLocationID != b.SourceLocationID || a.MediaVariantID != b.MediaVariantID || a.ConfiguredPath != b.ConfiguredPath || a.InventoryPath != b.InventoryPath || a.RelativePath != b.RelativePath || a.SizeBytes != b.SizeBytes {
		return false
	}
	at, errA := time.Parse(time.RFC3339Nano, a.Mtime)
	bt, errB := time.Parse(time.RFC3339Nano, b.Mtime)
	if errA != nil || errB != nil || !at.UTC().Truncate(time.Microsecond).Equal(bt.UTC().Truncate(time.Microsecond)) || len(a.SourceLinks) != len(b.SourceLinks) || len(a.Borrowers) != len(b.Borrowers) {
		return false
	}
	links := func(in []publicationSourceCleanupLink) []publicationSourceCleanupLink {
		out := append([]publicationSourceCleanupLink(nil), in...)
		sort.Slice(out, func(i, j int) bool {
			if out[i].TrackID != out[j].TrackID {
				return out[i].TrackID.String() < out[j].TrackID.String()
			}
			return out[i].ID.String() < out[j].ID.String()
		})
		return out
	}
	al, bl := links(a.SourceLinks), links(b.SourceLinks)
	for i := range al {
		x, y := al[i], bl[i]
		policyEqual := x.PolicyVersion == nil && y.PolicyVersion == nil || x.PolicyVersion != nil && y.PolicyVersion != nil && *x.PolicyVersion == *y.PolicyVersion
		if x.ID != y.ID || x.TrackID != y.TrackID || x.MediaVariantID != y.MediaVariantID || x.Selected != y.Selected || x.Compatible != y.Compatible || x.DecisionMethod != y.DecisionMethod || !policyEqual || !x.UpdatedAt.UTC().Truncate(time.Microsecond).Equal(y.UpdatedAt.UTC().Truncate(time.Microsecond)) {
			return false
		}
	}
	borrowers := func(in []publicationSourceCleanupWitness) []publicationSourceCleanupWitness {
		out := append([]publicationSourceCleanupWitness(nil), in...)
		sort.Slice(out, func(i, j int) bool { return out[i].PositionID.String() < out[j].PositionID.String() })
		return out
	}
	ab, bb := borrowers(a.Borrowers), borrowers(b.Borrowers)
	for i := range ab {
		x, y := ab[i], bb[i]
		if x.PositionID != y.PositionID || x.ReleaseID != y.ReleaseID || x.TrackID != y.TrackID || x.Generation != y.Generation || x.Epoch != y.Epoch || x.Generation == uuid.Nil || x.Epoch == uuid.Nil || x.ValidatedAt.IsZero() || !x.ValidatedAt.UTC().Truncate(time.Microsecond).Equal(y.ValidatedAt.UTC().Truncate(time.Microsecond)) {
			return false
		}
	}
	return true
}
