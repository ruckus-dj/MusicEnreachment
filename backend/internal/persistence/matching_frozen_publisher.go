package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublishFrozenMatchingEvaluationForDelivery publishes the sealed layout and
// enqueues apply only for its exact selected assignments.
func (repository *SetupManagerRepository) PublishFrozenMatchingEvaluationForDelivery(
	ctx context.Context,
	runID uuid.UUID,
	expectedOperationAttempt int,
	riverJobID int64,
	claim MatchingDeliveryClaim,
	publication FrozenMatchingPublication,
	expectedEvaluationCAS map[uuid.UUID]*string,
) (bool, error) {
	if repository == nil || runID == uuid.Nil || expectedOperationAttempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 || expectedEvaluationCAS == nil {
		return false, fmt.Errorf("publish frozen matching evaluation: delivery and evaluation CAS are required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock source mutations: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock MusicBrainz configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: read current MusicBrainz configuration: %w", err)
		}
		seal, calculation, err := validateFrozenMatchingPublicationAuthority(ctx, tx, runID, publication)
		if err != nil {
			return err
		}
		expected, err := BuildFrozenMatchingLayoutEvaluation(seal.LayoutDecision, publication.Inventory.Releases)
		if err != nil {
			return err
		}
		want, wantErr := json.Marshal(expected)
		got, gotErr := json.Marshal(publication.Evaluation)
		if wantErr != nil || gotErr != nil || !matchingJSONEqual(want, got) {
			return fmt.Errorf("publish frozen matching evaluation: caller differs from sealed layout decision")
		}
		unique := seal.LayoutDecision != nil && seal.LayoutDecision.Status == MatchingLayoutDecisionSelected
		becameStale := currentIdentity != seal.SourceConfiguration
		if !becameStale {
			err := recheckFrozenFallbackEvaluation(ctx, tx, seal)
			if err != nil && !errors.Is(err, ErrIncomingMatchConflict) {
				return err
			}
			becameStale = err != nil
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.State != "running" || operation.Attempt != expectedOperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return fmt.Errorf("publish frozen matching evaluation: operation delivery is no longer current")
		}
		var run struct {
			State                string `bun:"state"`
			ExecutionEpoch       int64  `bun:"execution_epoch"`
			CurrentRiverJobID    *int64 `bun:"current_river_job_id"`
			ActiveRiverJobID     *int64 `bun:"active_river_job_id"`
			ActiveRiverAttempt   *int   `bun:"active_river_attempt"`
			ActiveExecutionEpoch *int64 `bun:"active_execution_epoch"`
		}
		if err := tx.NewRaw(`SELECT state,execution_epoch,current_river_job_id,active_river_job_id,active_river_attempt,active_execution_epoch
			FROM matching_run WHERE id=? AND operation_id=? FOR UPDATE`, runID, runID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.CurrentRiverJobID == nil || *run.CurrentRiverJobID != riverJobID ||
			run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt ||
			run.ActiveExecutionEpoch == nil || *run.ActiveExecutionEpoch != claim.ExecutionEpoch {
			return fmt.Errorf("publish frozen matching evaluation: matching execution is no longer current")
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery d JOIN river_job j ON j.id=d.river_job_id
			WHERE d.run_id=? AND d.river_job_id=? AND d.operation_attempt=? AND d.consumed_at IS NULL
			AND j.state='running' AND j.attempt=?)`, runID, riverJobID, expectedOperationAttempt, claim.RiverAttempt).Scan(ctx, &registered); err != nil || !registered {
			return fmt.Errorf("publish frozen matching evaluation: delivery is not registered or was consumed")
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, runID).Scan(ctx, &pending); err != nil || pending != 0 {
			return fmt.Errorf("publish frozen matching evaluation: provider dependencies remain unsettled")
		}
		groupID := publication.Authority.GroupID
		if len(expectedEvaluationCAS) != 1 {
			return fmt.Errorf("publish frozen matching evaluation: evaluation CAS must identify exactly the frozen group")
		}
		if _, exists := expectedEvaluationCAS[groupID]; !exists {
			return fmt.Errorf("publish frozen matching evaluation: evaluation CAS group differs from frozen target")
		}
		if becameStale {
			if err := requestFrozenMatchingRefresh(ctx, tx, groupID); err != nil {
				return err
			}
			if err := settleFrozenEvaluationDelivery(ctx, tx, operation, claim, "stale"); err != nil {
				return err
			}
			changed = true
			return nil
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock evaluation CAS: %w", err)
		}
		var current sql.NullString
		err = tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &current)
		if errorsIsNoRows(err) {
			if expectedEvaluationCAS[groupID] != nil {
				return ErrIncomingMatchConflict
			}
		} else if err != nil {
			return fmt.Errorf("publish frozen matching evaluation: read evaluation CAS: %w", err)
		} else if !current.Valid || expectedEvaluationCAS[groupID] == nil || current.String != *expectedEvaluationCAS[groupID] {
			return ErrIncomingMatchConflict
		}
		var decision persistedMatchingDecision
		if unique {
			decision, err = frozenPersistedUniqueDecision(publication)
			if err != nil {
				return err
			}
			if _, _, err := BuildMatchingApplyInputSnapshotV2(seal, calculation, "pending", decision); err != nil {
				return fmt.Errorf("publish frozen matching evaluation: invalid unique decision: %w", err)
			}
		}
		evaluationRevision, err := persistFrozenReviewEvaluation(ctx, tx, publication, seal, decision)
		if err != nil {
			return err
		}
		if unique {
			if repository.riverInserter == nil {
				return fmt.Errorf("publish frozen matching evaluation: River inserter is required for unique apply admission")
			}
			if err := repository.enqueueFrozenMatchingApplyTx(ctx, tx, seal, calculation, evaluationRevision, claim.ExecutionEpoch, decision); err != nil {
				return err
			}
		}
		if err := settleFrozenEvaluationDelivery(ctx, tx, operation, claim, "succeeded"); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

func recheckFrozenFallbackEvaluation(ctx context.Context, tx bun.Tx, seal MatchingFallbackSealAuthority) error {
	if err := recheckFrozenFallbackProviderHistory(ctx, tx, seal); err != nil {
		return err
	}
	// No-query seals have no accepted history to lock their provider source.
	if seal.Disposition == "no_query" {
		var id uuid.UUID
		err := tx.NewRaw(`SELECT id FROM provider_source WHERE id=? AND provider_id=? AND endpoint=? AND configuration_identity=? FOR SHARE`,
			seal.SourceID, seal.ProviderID, seal.Endpoint, seal.SourceConfiguration).Scan(ctx, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		if err != nil {
			return err
		}
	}
	if _, err := RecheckMatchingLayoutTargetTx(ctx, tx, seal.Target.GroupID, MatchingLayoutPolicyVersion, seal.Intent.LayoutTarget); err != nil {
		return err
	}
	return validateMatchingRunFenceCurrentTarget(ctx, tx, seal.RunID)
}

func requestFrozenMatchingRefresh(ctx context.Context, tx bun.Tx, groupID uuid.UUID) error {
	var automatic bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM incoming_group g
		LEFT JOIN incoming_group_matching_authority a ON a.group_id=g.id
		WHERE g.id=? AND COALESCE(a.mode,'automatic')='automatic')`, groupID).Scan(ctx, &automatic); err != nil {
		return err
	}
	if !automatic {
		return nil
	}
	return RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{groupID})
}

func settleFrozenEvaluationDelivery(ctx context.Context, tx bun.Tx, operation *Operation, claim MatchingDeliveryClaim, stage string) error {
	now := time.Now().UTC()
	operation.State, operation.Stage = "succeeded", stage
	operation.SafeError, operation.ProviderDeliveryClaimedAt = nil, nil
	operation.FinishedAt, operation.UpdatedAt = &now, now
	if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "provider_delivery_claimed_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("publish frozen matching evaluation: complete operation: %w", err)
	}
	result, err := tx.NewRaw(`UPDATE matching_run SET state='succeeded',active_river_job_id=NULL,active_river_attempt=NULL,active_execution_epoch=NULL,updated_at=? WHERE id=? AND execution_epoch=?`,
		now, operation.ID, claim.ExecutionEpoch).Exec(ctx)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: complete run: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("publish frozen matching evaluation: run completion was not applied")
	}
	result, err = tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`,
		now, operation.ID, *operation.RiverJobID, operation.Attempt).Exec(ctx)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: consume delivery: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("publish frozen matching evaluation: delivery consumption was not applied")
	}
	return nil
}

func validateFrozenMatchingPublicationAuthority(ctx context.Context, tx bun.IDB, runID uuid.UUID, publication FrozenMatchingPublication) (MatchingFallbackSealAuthority, MatchingFallbackCalculationContext, error) {
	a := publication.Authority
	var seal MatchingFallbackSealAuthority
	if a.RunID != runID || a.WaitRevision < 1 || a.SealSchemaVersion != MatchingFrozenAuthorityVersion || a.TargetFenceVersion != 2 {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: caller authority identity is invalid")
	}
	var handoff struct {
		Phase    string `bun:"current_phase"`
		Revision int64  `bun:"revision"`
		Version  int    `bun:"provider_authority_version"`
		Digest   string `bun:"seal_digest"`
	}
	err := tx.NewRaw(`SELECT acquisition.current_phase,acquisition.revision,handoff.provider_authority_version,encode(handoff.seal_digest,'hex') AS seal_digest
		FROM matching_acquisition acquisition JOIN matching_fallback_evaluation_handoff handoff ON handoff.run_id=acquisition.run_id
		WHERE acquisition.run_id=? AND handoff.wait_revision=?`, runID, a.WaitRevision).Scan(ctx, &handoff)
	if err != nil || handoff.Phase != MatchingAcquisitionFallbackEvaluationSealed || handoff.Revision != a.WaitRevision+1 || handoff.Version != MatchingFrozenAuthorityVersion {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback evaluation handoff is not current")
	}
	derived, found, err := buildMatchingFallbackSealAuthority(ctx, tx, runID, a.WaitRevision, true)
	if err != nil || !found {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback wait is not current")
	}
	if handoff.Digest != derived.Digest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback handoff seal identity is invalid")
	}
	var stored struct {
		Schema  int             `bun:"schema_version"`
		Payload json.RawMessage `bun:"authority"`
		Digest  []byte          `bun:"authority_digest"`
	}
	if err := tx.NewRaw(`SELECT schema_version,authority,authority_digest FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`, runID, a.WaitRevision).Scan(ctx, &stored); err != nil {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: read persisted authority seal: %w", err)
	}
	if stored.Schema != MatchingFrozenAuthorityVersion || !equalDigest(stored.Digest, mustDecodeSHA256(derived.Digest)) || derived.Digest != a.SealDigest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted seal digest does not match caller authority")
	}
	var storedAuthority MatchingFallbackSealAuthority
	if err := json.Unmarshal(stored.Payload, &storedAuthority); err != nil || storedAuthority.Digest != derived.Digest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted seal payload is invalid")
	}
	if !frozenAuthorityMatches(a, derived) {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: caller authority differs from persisted seal")
	}
	if derived.SelectedCalculation == nil || len(derived.FullEvaluationContext) == 0 {
		if derived.SelectedCalculation != nil || len(derived.FullEvaluationContext) != 0 || len(publication.Inventory.Releases) != 0 ||
			publication.Evaluation.SelectedRelease != "" || len(publication.Evaluation.Assignments) != 0 {
			return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: sealed calculation context is unavailable")
		}
		return derived, MatchingFallbackCalculationContext{}, nil
	}
	calculation, err := getMatchingFallbackCalculationReceipt(ctx, tx, runID, derived.SelectedCalculation.AcquisitionRevision)
	var snapshot MatchingFallbackCalculationContext
	if err != nil || calculation == nil || calculation.StageIndex != derived.SelectedCalculation.StageIndex || json.Unmarshal(calculation.Context, &snapshot) != nil ||
		!matchingJSONEqual(calculation.Context, derived.FullEvaluationContext) {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: sealed calculation context is unavailable")
	}
	acceptedForReview := append(append([]MatchingFallbackLookupReceipt(nil), snapshot.LookupReceipts...), derived.ReviewLookupReceipts...)
	if !frozenInventoryMatchesAcceptedProjections(publication.Inventory.Releases, acceptedForReview) {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: review inventory differs from accepted release projections")
	}
	return derived, snapshot, nil
}

func frozenInventoryMatchesAcceptedProjections(inventory []MatchingRelease, receipts []MatchingFallbackLookupReceipt) bool {
	accepted := make([]MatchingRelease, 0, len(receipts))
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		var projection ProviderMatchingSnapshot
		if json.Unmarshal(receipt.AcceptedProjection, &projection) != nil || len(projection.Releases) != 1 {
			return false
		}
		release := projection.Releases[0]
		if release.ID == uuid.Nil || release.ProviderKey == "" || seen[release.ProviderKey] {
			return false
		}
		seen[release.ProviderKey] = true
		accepted = append(accepted, release)
	}
	if len(accepted) != len(inventory) {
		return false
	}
	if len(accepted) == 0 {
		return true
	}
	accepted = FrozenMatchingReviewReleases(accepted)
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].ProviderKey < accepted[j].ProviderKey })
	actual := append([]MatchingRelease(nil), inventory...)
	for i := range actual {
		if len(actual[i].Tracks) == 0 {
			actual[i].Tracks = nil
		}
	}
	sort.Slice(actual, func(i, j int) bool { return actual[i].ProviderKey < actual[j].ProviderKey })
	left, leftErr := json.Marshal(accepted)
	right, rightErr := json.Marshal(actual)
	return leftErr == nil && rightErr == nil && matchingJSONEqual(left, right)
}

func frozenAuthorityMatches(caller FrozenMatchingAuthorityIdentity, derived MatchingFallbackSealAuthority) bool {
	if caller.SealSchemaVersion != derived.SchemaVersion || caller.RunID != derived.RunID || caller.WaitRevision != derived.WaitRevision || caller.SealDigest != derived.Digest ||
		caller.SealDisposition != derived.Disposition || caller.TargetFenceVersion != derived.Target.Version || caller.TargetCaptureDigest != derived.Target.CaptureDigest ||
		caller.SourceID != derived.SourceID || caller.ProviderID != derived.ProviderID || caller.Endpoint != derived.Endpoint || caller.SourceConfiguration != derived.SourceConfiguration ||
		caller.RunProviderConfiguration != derived.RunProviderConfiguration || caller.GroupID != derived.Target.GroupID || caller.GroupRevision != derived.Target.GroupRevision ||
		caller.GroupAuthority != derived.GroupAuthority || !matchingJSONEqual(caller.TargetCapture, derived.Target.Capture) {
		return false
	}
	var context MatchingFallbackCalculationContext
	if len(derived.FullEvaluationContext) == 0 {
		if len(caller.FullEvaluationContext) != 0 || derived.SelectedCalculation != nil || caller.RunSourceID != derived.SourceID || caller.RunProviderID != derived.ProviderID ||
			caller.RunEndpoint != derived.Endpoint || caller.RunConfiguration != derived.RunProviderConfiguration {
			return false
		}
	} else if json.Unmarshal(derived.FullEvaluationContext, &context) != nil || caller.RunSourceID != context.RunSourceID || caller.RunProviderID != context.RunProviderID ||
		caller.RunEndpoint != context.RunEndpoint || caller.RunConfiguration != context.RunConfiguration || caller.CalculationStageIndex != context.StageIndex ||
		!matchingJSONEqual(caller.FullEvaluationContext, derived.FullEvaluationContext) {
		return false
	}
	leftIntent, _ := json.Marshal(caller.Intent)
	rightIntent, _ := json.Marshal(derived.Intent)
	return matchingJSONEqual(leftIntent, rightIntent)
}

func frozenPersistedUniqueDecision(publication FrozenMatchingPublication) (persistedMatchingDecision, error) {
	var decision persistedMatchingDecision
	if publication.Evaluation.Outcome != "unique" || publication.Evaluation.Decision != "apply" || publication.Evaluation.SelectedRelease == "" || len(publication.Evaluation.Assignments) == 0 {
		return decision, fmt.Errorf("publish frozen matching evaluation: unique decision is incomplete")
	}
	var release *MatchingRelease
	for index := range publication.Inventory.Releases {
		if strings.EqualFold(publication.Inventory.Releases[index].ProviderKey, publication.Evaluation.SelectedRelease) {
			if release != nil {
				return decision, fmt.Errorf("publish frozen matching evaluation: selected release is ambiguous")
			}
			release = &publication.Inventory.Releases[index]
		}
	}
	if release == nil || release.ID == uuid.Nil {
		return decision, fmt.Errorf("publish frozen matching evaluation: selected release is outside the accepted inventory")
	}
	decision.Version = 1
	decision.GroupID = publication.Inventory.GroupID
	decision.PolicyVersion = publication.Authority.Intent.PolicyVersion
	decision.Outcome = "unique"
	decision.SelectedRelease = &struct {
		ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
		ProviderReleaseKey string    `json:"provider_release_key"`
	}{ProviderReleaseID: release.ID, ProviderReleaseKey: release.ProviderKey}
	decision.Assignments = append([]FrozenMatchingAssignment{}, publication.Evaluation.Assignments...)
	return decision, nil
}

func (repository *SetupManagerRepository) enqueueFrozenMatchingApplyTx(
	ctx context.Context,
	tx bun.Tx,
	seal MatchingFallbackSealAuthority,
	calculation MatchingFallbackCalculationContext,
	evaluationRevision string,
	parentExecutionEpoch int64,
	decision persistedMatchingDecision,
) error {
	snapshot, _, err := BuildMatchingApplyInputSnapshotV2(seal, calculation, evaluationRevision, decision)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: build frozen apply snapshot: %w", err)
	}
	inputDigest, err := MatchingApplyInputSnapshotV2Digest(snapshot)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: digest frozen apply snapshot: %w", err)
	}
	input, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: encode frozen apply snapshot: %w", err)
	}
	operationID := uuid.New()
	operation := &Operation{ID: operationID, Kind: MatchingApplyOperationKind, State: "queued", Stage: "queued", InputSnapshot: input}
	if err := repository.createOperationAndEnqueueTx(ctx, tx, operation, repository.riverInserter, MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
		return fmt.Errorf("publish frozen matching evaluation: enqueue frozen apply: %w", err)
	}
	inputHash, _ := hex.DecodeString(inputDigest)
	sealHash, _ := hex.DecodeString(seal.Digest)
	captureHash, _ := hex.DecodeString(seal.Target.CaptureDigest)
	_, err = tx.NewRaw(`INSERT INTO matching_apply_request
		(group_id,parent_run_id,evaluation_revision,apply_operation_id,schema_version,input_sha256,frozen_authority_version,parent_execution_epoch,seal_wait_revision,seal_digest,target_capture_sha256)
		VALUES (?,?,?,?,2,?,?,?,?,?,?)`, seal.Target.GroupID, seal.RunID, evaluationRevision, operationID, inputHash, MatchingFrozenAuthorityVersion, parentExecutionEpoch, seal.WaitRevision, sealHash, captureHash).Exec(ctx)
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: persist frozen apply request: %w", err)
	}
	return nil
}

func persistFrozenReviewEvaluation(ctx context.Context, tx bun.Tx, publication FrozenMatchingPublication, seal MatchingFallbackSealAuthority, decision persistedMatchingDecision) (string, error) {
	groupID := publication.Authority.GroupID
	if groupID != seal.Target.GroupID || publication.Inventory.GroupID != groupID || publication.Inventory.GroupRevision != seal.Target.GroupRevision || len(publication.Inventory.Members) == 0 {
		return "", fmt.Errorf("publish frozen matching evaluation: frozen review inventory identity is invalid")
	}
	if !sameFrozenCapturedMetadata(publication.Inventory.CapturedMetadata, seal.CapturedMetadata) {
		return "", fmt.Errorf("publish frozen matching evaluation: captured member metadata differs from persisted seal")
	}
	if !frozenInventoryMatchesTarget(publication.Inventory, seal.Target.Capture, seal.CapturedMetadata) {
		return "", fmt.Errorf("publish frozen matching evaluation: frozen target inventory differs from persisted capture")
	}
	if _, err := tx.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, groupID).Exec(ctx); err != nil {
		return "", fmt.Errorf("publish frozen matching evaluation: replace prior review: %w", err)
	}
	providerFences, _ := json.Marshal(map[string]any{"frozen_authority_version": MatchingFrozenAuthorityVersion, "run_id": seal.RunID, "wait_revision": seal.WaitRevision, "seal_digest": seal.Digest})
	evidenceValue := map[string]any{"frozen_publication": publication, "review_proposal": frozenReviewProposal(publication)}
	if decision.Version != 0 {
		evidenceValue["evaluation_decision"] = decision
	}
	evidence, err := json.Marshal(evidenceValue)
	if err != nil {
		return "", fmt.Errorf("publish frozen matching evaluation: encode review evidence: %w", err)
	}
	evaluationRevision := uuid.NewString()
	sealDigest, _ := hex.DecodeString(seal.Digest)
	if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation
		(group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,
		provider_source_id,provider_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,
		provider_snapshot_revision,provider_fences,matching_authority_mode,matching_authority_revision,fence_version,target_run_id,target_capture,target_capture_digest,
		frozen_authority_version,frozen_run_id,frozen_wait_revision,frozen_seal_digest)
		VALUES(?,?,?::jsonb,?,?,0,?,?,?,?,?,?,NULL,NULL,NULL,?::jsonb,?,?,2,?,?,?,?,?,?,?)`, groupID, evaluationRevision, string(evidence), seal.Target.GroupRevision,
		"frozen:"+seal.Digest, seal.Intent.PolicyVersion, seal.SourceID, seal.ProviderID, seal.RunProviderConfiguration, "frozen", seal.Endpoint, string(providerFences), seal.GroupAuthority.Mode, seal.GroupAuthority.Revision,
		seal.RunID, string(seal.Target.Capture), mustDecodeSHA256(seal.Target.CaptureDigest), MatchingFrozenAuthorityVersion, seal.RunID, seal.WaitRevision, sealDigest).Exec(ctx); err != nil {
		return "", fmt.Errorf("publish frozen matching evaluation: insert frozen header: %w", err)
	}
	memberByID := make(map[uuid.UUID]FrozenMatchingReviewMember, len(publication.Inventory.Members))
	for _, member := range publication.Inventory.Members {
		if member.FileMemberID == uuid.Nil || member.WorkID == uuid.Nil {
			return "", fmt.Errorf("publish frozen matching evaluation: invalid review member")
		}
		if _, exists := memberByID[member.FileMemberID]; exists {
			return "", fmt.Errorf("publish frozen matching evaluation: duplicate review member")
		}
		memberByID[member.FileMemberID] = member
		var captured *MatchingAcquisitionCapturedMember
		for i := range seal.CapturedMetadata {
			if seal.CapturedMetadata[i].MemberID == member.FileMemberID {
				captured = &seal.CapturedMetadata[i]
				break
			}
		}
		if captured == nil || captured.WorkID != member.WorkID || !matchingJSONEqual(captured.Tags, member.Tags) {
			return "", fmt.Errorf("publish frozen matching evaluation: member fence differs from persisted capture")
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_member(group_id,file_member_id,work_id,source_sha256) VALUES(?,?,?,?)`, groupID, member.FileMemberID, member.WorkID, member.SourceSHA256).Exec(ctx); err != nil {
			return "", fmt.Errorf("publish frozen matching evaluation: insert review member: %w", err)
		}
	}
	if len(memberByID) != len(seal.Intent.CapturedMembers) {
		return "", fmt.Errorf("publish frozen matching evaluation: review members differ from sealed intent")
	}
	return evaluationRevision, nil
}

func frozenInventoryMatchesTarget(inventory FrozenMatchingReviewInventory, target json.RawMessage, metadata []MatchingAcquisitionCapturedMember) bool {
	var capture struct {
		Members []struct {
			VariantID uuid.UUID                        `json:"variant_id"`
			Locations []IncomingGroupingMemberLocation `json:"locations"`
			Captures  []IncomingGroupingCapture        `json:"captures"`
		} `json:"members"`
	}
	if json.Unmarshal(target, &capture) != nil || len(capture.Members) != len(inventory.Members) {
		return false
	}
	expectedCaptures := make([]IncomingGroupingCapture, 0)
	memberByID := make(map[uuid.UUID]FrozenMatchingReviewMember, len(inventory.Members))
	for _, member := range inventory.Members {
		if member.FileMemberID == uuid.Nil || memberByID[member.FileMemberID].FileMemberID != uuid.Nil {
			return false
		}
		memberByID[member.FileMemberID] = member
	}
	metadataByID := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(metadata))
	for _, item := range metadata {
		metadataByID[item.MemberID] = item
	}
	for _, targetMember := range capture.Members {
		member, exists := memberByID[targetMember.VariantID]
		captured, hasMetadata := metadataByID[targetMember.VariantID]
		if !exists || !hasMetadata || len(targetMember.Captures) == 0 || captured.WorkID != member.WorkID || !matchingJSONEqual(captured.Tags, member.Tags) {
			return false
		}
		first := targetMember.Captures[0]
		if !bytes.Equal(first.SourceSHA256, member.SourceSHA256) {
			return false
		}
		var locations []struct {
			RootID         uuid.UUID `json:"RootID"`
			WorkID         uuid.UUID `json:"WorkID"`
			LocationID     uuid.UUID `json:"LocationID"`
			ConfiguredPath string    `json:"ConfiguredPath"`
			InventoryPath  string    `json:"InventoryPath"`
			RelativePath   string    `json:"RelativePath"`
			SizeBytes      int64     `json:"SizeBytes"`
			Mtime          string    `json:"Mtime"`
		}
		if json.Unmarshal(member.Locations, &locations) != nil || len(locations) != len(targetMember.Locations) {
			return false
		}
		for index, expected := range targetMember.Locations {
			actual := locations[index]
			if actual.RootID != expected.RootID || actual.WorkID != expected.WorkID || actual.LocationID != expected.LocationID ||
				actual.ConfiguredPath != expected.ConfiguredPath || actual.InventoryPath != expected.InventoryPath || actual.RelativePath != expected.RelativePath || actual.SizeBytes != expected.SizeBytes ||
				actual.Mtime != expected.Mtime.Format(time.RFC3339Nano) {
				return false
			}
		}
		expectedCaptures = append(expectedCaptures, targetMember.Captures...)
		delete(memberByID, targetMember.VariantID)
	}
	left, leftErr := json.Marshal(expectedCaptures)
	right, rightErr := json.Marshal(inventory.Captures)
	return len(memberByID) == 0 && leftErr == nil && rightErr == nil && matchingJSONEqual(left, right)
}

func frozenReviewProposal(publication FrozenMatchingPublication) json.RawMessage {
	type layout struct {
		ProviderReleaseID uuid.UUID        `json:"provider_release_id"`
		ReleaseID         string           `json:"release_id"`
		Title             string           `json:"title"`
		TracklistComplete bool             `json:"tracklist_complete"`
		Positions         []map[string]any `json:"positions"`
	}
	releases := make([]layout, 0, len(publication.Inventory.Releases))
	for _, release := range publication.Inventory.Releases {
		positions := make([]map[string]any, 0, len(release.Tracks))
		for _, track := range release.Tracks {
			title := track.TrackTitle
			if title == "" {
				title = track.RecordingTitle
			}
			positions = append(positions, map[string]any{"track_id": track.ID, "medium": track.Medium, "position": track.Position,
				"displayed_number": track.DisplayedNumber, "recording_id": track.RecordingProviderKey, "title": title})
		}
		releases = append(releases, layout{ProviderReleaseID: release.ID, ReleaseID: release.ProviderKey, Title: release.Name, TracklistComplete: release.Complete, Positions: positions})
	}
	selected := any(nil)
	if publication.Evaluation.SelectedRelease != "" {
		for _, release := range releases {
			if release.ReleaseID == publication.Evaluation.SelectedRelease {
				selected = release
				break
			}
		}
	}
	files := make([]map[string]any, 0, len(publication.Inventory.Members))
	for _, member := range publication.Inventory.Members {
		var rawTags map[string][]string
		if json.Unmarshal(member.Tags, &rawTags) != nil {
			rawTags = map[string][]string{}
		}
		files = append(files, map[string]any{"file_member_id": member.FileMemberID, "raw_tags": rawTags, "diagnostics": []any{}})
	}
	encoded, _ := json.Marshal(map[string]any{"version": 1, "group_id": publication.Authority.GroupID, "selected_release": selected, "releases": releases, "files": files})
	return encoded
}

func sameFrozenCapturedMetadata(left, right []MatchingAcquisitionCapturedMember) bool {
	if len(left) != len(right) {
		return false
	}
	leftByID := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(left))
	for _, item := range left {
		if item.MemberID == uuid.Nil {
			return false
		}
		if _, exists := leftByID[item.MemberID]; exists {
			return false
		}
		leftByID[item.MemberID] = item
	}
	for _, item := range right {
		other, exists := leftByID[item.MemberID]
		if !exists {
			return false
		}
		leftJSON, leftErr := json.Marshal(other)
		rightJSON, rightErr := json.Marshal(item)
		if leftErr != nil || rightErr != nil || !matchingJSONEqual(leftJSON, rightJSON) {
			return false
		}
		delete(leftByID, item.MemberID)
	}
	return len(leftByID) == 0
}

func errorsIsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
