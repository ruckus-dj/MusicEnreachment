package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/uptrace/bun"
)

// AdvanceFallbackApplicabilityForDelivery commits the disposition of a saved
// applicability calculation under the delivery fence that owns its phase.
// preparedNextSearch is required only when another immutable query-plan stage
// must be admitted; it is never treated as authority without validation below.
func (repository *SetupManagerRepository) AdvanceFallbackApplicabilityForDelivery(
	ctx context.Context, client RiverInserter, runID uuid.UUID, attempt int, riverJobID int64,
	claim MatchingDeliveryClaim, expectedRevision int64, preparedNextSearch *MatchingProviderRequest,
) (bool, error) {
	if client == nil || runID == uuid.Nil || attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 ||
		claim.RiverAttempt < 1 || expectedRevision < 1 {
		return false, fmt.Errorf("advance fallback applicability: invalid delivery identity")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Keep lock ordering aligned with other matching-delivery writers.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("advance fallback applicability: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("advance fallback applicability: lock source mutations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("advance fallback applicability: lock provider configuration: %w", err)
		}
		var authority struct {
			SourceID uuid.UUID `bun:"provider_source_id"`
			Identity string    `bun:"provider_configuration_identity"`
			Fence    int       `bun:"fence_version"`
		}
		if err := tx.NewRaw(`SELECT provider_source_id,provider_configuration_identity,fence_version FROM matching_run WHERE id=?`, runID).Scan(ctx, &authority); err != nil {
			return fmt.Errorf("advance fallback applicability: read run authority: %w", err)
		}
		var configIdentity, sourceIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil {
			return fmt.Errorf("advance fallback applicability: read configuration: %w", err)
		}
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, authority.SourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("advance fallback applicability: lock source: %w", err)
		}
		if sourceIdentity != authority.Identity || configIdentity != authority.Identity {
			return nil
		}
		if err := validateMatchingRunFence(ctx, tx, runID, authority.Fence); err != nil {
			return err
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation,current_group`, runID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("advance fallback applicability: lock target groups: %w", err)
		}
		var stale bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
			generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))`, runID).Scan(ctx, &stale); err != nil {
			return fmt.Errorf("advance fallback applicability: validate target generations: %w", err)
		}
		if stale {
			return nil
		}
		op, err := repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		if op.Kind != "matching_evaluation_v1" || op.Attempt != attempt || op.State != "running" || op.RiverJobID == nil || *op.RiverJobID != riverJobID {
			return nil
		}
		var current struct {
			Revision      int64  `bun:"revision"`
			Phase         string `bun:"current_phase"`
			State         string `bun:"state"`
			Epoch         int64  `bun:"execution_epoch"`
			ActiveEpoch   *int64 `bun:"active_execution_epoch"`
			ActiveJob     *int64 `bun:"active_river_job_id"`
			ActiveAttempt *int   `bun:"active_river_attempt"`
			CurrentJob    *int64 `bun:"current_river_job_id"`
		}
		if err := tx.NewRaw(`SELECT acquisition.revision,acquisition.current_phase,run.state,run.execution_epoch,
			run.active_execution_epoch,run.active_river_job_id,run.active_river_attempt,run.current_river_job_id
			FROM matching_run run JOIN matching_acquisition acquisition ON acquisition.run_id=run.id WHERE run.id=? FOR UPDATE OF run`, runID).
			Scan(ctx, &current.Revision, &current.Phase, &current.State, &current.Epoch, &current.ActiveEpoch, &current.ActiveJob, &current.ActiveAttempt, &current.CurrentJob); err != nil {
			return fmt.Errorf("advance fallback applicability: lock delivery owner: %w", err)
		}
		if current.Revision != expectedRevision || current.Phase != MatchingAcquisitionFallbackApplicability || current.State != "running" ||
			current.Epoch != claim.ExecutionEpoch || current.ActiveEpoch == nil || *current.ActiveEpoch != claim.ExecutionEpoch ||
			current.ActiveJob == nil || *current.ActiveJob != riverJobID || current.ActiveAttempt == nil || *current.ActiveAttempt != claim.RiverAttempt ||
			current.CurrentJob == nil || *current.CurrentJob != riverJobID {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_delivery delivery JOIN river_job ON river_job.id=delivery.river_job_id
			WHERE delivery.run_id=? AND delivery.operation_attempt=? AND delivery.river_job_id=? AND delivery.consumed_at IS NULL
			AND river_job.state='running' AND river_job.attempt=?)`, runID, attempt, riverJobID, claim.RiverAttempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("advance fallback applicability: verify delivery: %w", err)
		}
		if !registered {
			return nil
		}
		var saved struct {
			Stage         int             `bun:"stage_index"`
			Digest        string          `bun:"input_digest"`
			Context       json.RawMessage `bun:"context"`
			Edges         json.RawMessage `bun:"edge_calculations"`
			Schema        int             `bun:"schema_version"`
			Calculator    int             `bun:"calculator_version"`
			DigestVersion int             `bun:"digest_version"`
		}
		if err := tx.NewRaw(`SELECT stage_index,encode(input_digest,'hex'),context,edge_calculations,schema_version,calculator_version,digest_version
			FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=? FOR SHARE`, runID, expectedRevision).
			Scan(ctx, &saved.Stage, &saved.Digest, &saved.Context, &saved.Edges, &saved.Schema, &saved.Calculator, &saved.DigestVersion); err != nil {
			return fmt.Errorf("advance fallback applicability: load saved calculation: %w", err)
		}
		legacyReceipt := saved.Schema == matchingFallbackCalculationLegacySchemaVersion && saved.Calculator == matchingFallbackCalculationLegacyCalculatorVersion && saved.DigestVersion == matchingFallbackCalculationLegacyDigestVersion && saved.Stage == 0
		if !legacyReceipt && (saved.Schema != matchingFallbackCalculationSchemaVersion || saved.Calculator != matchingFallbackCalculationCalculatorVersion || saved.DigestVersion != matchingFallbackCalculationDigestVersion) {
			return fmt.Errorf("advance fallback applicability: saved calculation version is unsupported")
		}
		var snapshot MatchingFallbackCalculationContext
		if json.Unmarshal(saved.Context, &snapshot) != nil || snapshot.RunID != runID || snapshot.AcquisitionRevision != expectedRevision || snapshot.StageIndex != saved.Stage {
			return fmt.Errorf("advance fallback applicability: saved calculation context is invalid")
		}
		persisted, exists, err := loadMatchingFallbackCalculationContext(ctx, tx, runID, expectedRevision)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: reconstruct saved authority: %w", err)
		}
		if !exists {
			return nil
		}
		canonicalSaved, err := canonicalMatchingFallbackCalculationContextBytes(snapshot)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: canonicalize saved context: %w", err)
		}
		canonicalPersisted, err := canonicalMatchingFallbackCalculationContextBytes(persisted)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: canonicalize persisted context: %w", err)
		}
		actualDigest, digestErr := MatchingFallbackCalculationInputDigest(snapshot)
		if legacyReceipt {
			canonicalSaved, err = canonicalMatchingFallbackCalculationContextBytesV2(snapshot)
			if err != nil {
				return fmt.Errorf("advance fallback applicability: canonicalize legacy context: %w", err)
			}
			canonicalPersisted, err = canonicalMatchingFallbackCalculationContextBytesV2(persisted)
			if err != nil {
				return fmt.Errorf("advance fallback applicability: canonicalize legacy authority: %w", err)
			}
			actualDigest, digestErr = MatchingFallbackCalculationInputDigestV2(snapshot)
		}
		if digestErr != nil || actualDigest != saved.Digest || !equalDigest(canonicalSaved, canonicalPersisted) {
			return fmt.Errorf("advance fallback applicability: saved context binding is invalid")
		}
		if err := ValidateMatchingFallbackCalculationEdges(snapshot, saved.Edges); err != nil {
			return fmt.Errorf("advance fallback applicability: validate saved edges: %w", err)
		}
		var edges []struct {
			Confidence float64 `json:"confidence"`
		}
		if json.Unmarshal(saved.Edges, &edges) != nil {
			return fmt.Errorf("advance fallback applicability: saved edges are invalid")
		}
		qualified := false
		for _, edge := range edges {
			if edge.Confidence >= 0.70 {
				qualified = true
				break
			}
		}
		var intent MatchingAcquisitionIntent
		var intentBytes []byte
		if err := tx.NewRaw(`SELECT intent FROM matching_acquisition WHERE run_id=?`, runID).Scan(ctx, &intentBytes); err != nil || json.Unmarshal(intentBytes, &intent) != nil {
			return fmt.Errorf("advance fallback applicability: saved acquisition intent is invalid")
		}
		nextPhase, runState, operationStage, input, output := "", "", "", json.RawMessage(nil), json.RawMessage(nil)
		var children []MatchingProviderRequest
		if qualified {
			if preparedNextSearch != nil {
				return fmt.Errorf("advance fallback applicability: qualified result cannot admit another search")
			}
			nextPhase, runState, operationStage = "pending_seal", "waiting_policy", "pending_seal"
			input, _ = json.Marshal(map[string]any{"calculation_revision": expectedRevision, "stage_index": saved.Stage})
			output, _ = json.Marshal(map[string]any{"disposition": "waiting_policy", "reason": "qualified_candidate"})
		} else {
			var plan policy.TypedFallbackQueryPlan
			if json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil || saved.Stage < 0 || saved.Stage >= len(plan.Stages) {
				return fmt.Errorf("advance fallback applicability: immutable fallback stage is unsupported")
			}
			if saved.Stage+1 < len(plan.Stages) {
				if preparedNextSearch == nil || preparedNextSearch.Operation == nil || preparedNextSearch.Operation.ProviderExplicitRefresh {
					return fmt.Errorf("advance fallback applicability: exact prepared next search is required")
				}
				stageDescriptor := plan.Stages[saved.Stage+1]
				kind, prefix, ok := policy.ProviderKindForFallbackStage(stageDescriptor.Kind)
				op := preparedNextSearch.Operation
				var request struct {
					Schema int    `json:"schema_version"`
					Query  string `json:"query"`
					Offset int    `json:"offset"`
					Limit  int    `json:"limit"`
				}
				if !ok || kind != op.Kind || op.ID == uuid.Nil || op.State != "queued" || op.ProviderID == nil || *op.ProviderID != snapshot.RunProviderID || op.ProviderEndpoint != snapshot.RunEndpoint ||
					op.ProviderSourceID == nil || *op.ProviderSourceID != snapshot.RunSourceID || op.ProviderConfigurationIdentity == nil || *op.ProviderConfigurationIdentity != snapshot.RunConfiguration || op.ProviderCacheKey == nil || json.Unmarshal(op.InputSnapshot, &request) != nil ||
					request.Schema != 1 || request.Query != stageDescriptor.Query || request.Offset != 0 || request.Limit != 10 || stageDescriptor.Offset != 0 || stageDescriptor.Limit != 10 ||
					*op.ProviderCacheKey != fmt.Sprintf("%s:0:10:%s", prefix, stageDescriptor.Query) ||
					!fallbackStageMatchesRequest(intent, "search", op.Kind, op.InputSnapshot, saved.Stage+1) {
					return fmt.Errorf("advance fallback applicability: prepared search differs from immutable next stage")
				}
				children = []MatchingProviderRequest{*preparedNextSearch}
				nextPhase, runState, operationStage = MatchingAcquisitionFallbackSearch, "waiting_provider", "waiting_provider"
				input, _ = json.Marshal(map[string]any{"stage_index": saved.Stage + 1, "previous_calculation_revision": expectedRevision})
				output, _ = json.Marshal(map[string]any{"request_id": op.ID, "cache_key": *op.ProviderCacheKey})
			} else {
				if preparedNextSearch != nil {
					return fmt.Errorf("advance fallback applicability: exhausted plan cannot admit another search")
				}
				history, err := validateFallbackApplicabilityZeroHistory(ctx, tx, runID, saved.Stage)
				if err != nil {
					return fmt.Errorf("advance fallback applicability: validate exhausted-stage history: %w", err)
				}
				nextPhase, runState, operationStage = "fallback_review_wait", "waiting_policy", "review_pending"
				input, _ = json.Marshal(map[string]any{"calculation_revision": expectedRevision, "stage_index": saved.Stage})
				output, _ = json.Marshal(map[string]any{"disposition": "waiting_policy", "reason": "review_required", "calculation_receipts": history})
			}
		}
		if len(children) == 1 {
			child := children[0].Operation
			if err := validateFallbackSearchRiverArgs(preparedNextSearch.Args, child.ID); err != nil {
				return fmt.Errorf("advance fallback applicability: prepared search River arguments are invalid: %w", err)
			}
			if err := repository.createOperationAndEnqueueTx(ctx, tx, child, client, preparedNextSearch.Args, nil); err != nil {
				return fmt.Errorf("advance fallback applicability: enqueue next stage: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES (?,?,?)`, runID, child.ID, *child.ProviderCacheKey).Exec(ctx); err != nil {
				return fmt.Errorf("advance fallback applicability: register next-stage dependency: %w", err)
			}
			if err := registerFallbackRequest(ctx, tx, runID, saved.Stage+1, "search", snapshot.RunSourceID, snapshot.RunConfiguration, child); err != nil {
				return fmt.Errorf("advance fallback applicability: register next-stage intent: %w", err)
			}
		}
		phase := MatchingAcquisitionPhase{Revision: expectedRevision + 1, Phase: nextPhase, Input: input, Output: output, ResponseProvenance: json.RawMessage("[]")}
		cursorResult, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase=?,revision=? WHERE run_id=? AND revision=? AND current_phase=?`, phase.Phase, phase.Revision, runID, expectedRevision, MatchingAcquisitionFallbackApplicability).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: update cursor: %w", err)
		}
		if affected, err := cursorResult.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("advance fallback applicability: acquisition cursor changed before commit")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance) VALUES (?,?,?,?::jsonb,?::jsonb,'[]'::jsonb)`, runID, phase.Revision, phase.Phase, string(phase.Input), string(phase.Output)).Exec(ctx); err != nil {
			return fmt.Errorf("advance fallback applicability: append phase: %w", err)
		}
		now := time.Now().UTC()
		consume, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, now, runID, riverJobID, attempt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: consume delivery: %w", err)
		}
		if affected, err := consume.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("advance fallback applicability: active delivery was not consumed")
		}
		park, err := tx.NewRaw(`UPDATE matching_run SET state=?,active_river_job_id=NULL,active_river_attempt=NULL,active_execution_epoch=NULL,updated_at=? WHERE id=? AND execution_epoch=? AND active_river_job_id=? AND active_execution_epoch=?`, runState, now, runID, claim.ExecutionEpoch, riverJobID, claim.ExecutionEpoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance fallback applicability: update run: %w", err)
		}
		if affected, err := park.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("advance fallback applicability: active run claim changed before commit")
		}
		op.Stage, op.UpdatedAt = operationStage, now
		if _, err := tx.NewUpdate().Model(op).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("advance fallback applicability: update operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

func validateFallbackSearchRiverArgs(args river.JobArgs, operationID uuid.UUID) error {
	if args == nil || args.Kind() != "provider_fetch_v1" || operationID == uuid.Nil {
		return fmt.Errorf("provider fetch arguments and operation identity are required")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode arguments: %w", err)
	}
	var decoded struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode ID-only arguments: %w", err)
	}
	if decoded.OperationID != operationID {
		return fmt.Errorf("arguments operation ID differs from prepared operation")
	}
	return nil
}

type fallbackApplicabilityHistoryReceipt struct {
	StageIndex  int    `json:"stage_index"`
	Revision    int64  `json:"revision"`
	InputDigest string `json:"input_digest"`
}

// validateFallbackApplicabilityZeroHistory proves that every executed stage has
// an immutable, complete, zero-confidence receipt before review is selected.
func validateFallbackApplicabilityZeroHistory(ctx context.Context, tx bun.Tx, runID uuid.UUID, lastStage int) ([]fallbackApplicabilityHistoryReceipt, error) {
	history := make([]fallbackApplicabilityHistoryReceipt, 0, lastStage+1)
	for stage := 0; stage <= lastStage; stage++ {
		var saved struct {
			Revision      int64           `bun:"acquisition_revision"`
			Stage         int             `bun:"stage_index"`
			Digest        string          `bun:"input_digest"`
			Context       json.RawMessage `bun:"context"`
			Edges         json.RawMessage `bun:"edge_calculations"`
			Schema        int             `bun:"schema_version"`
			Calculator    int             `bun:"calculator_version"`
			DigestVersion int             `bun:"digest_version"`
		}
		if err := tx.NewRaw(`SELECT acquisition_revision,stage_index,encode(input_digest,'hex'),context,edge_calculations,
			schema_version,calculator_version,digest_version FROM matching_fallback_calculation_receipt
			WHERE run_id=? AND stage_index=?`, runID, stage).Scan(ctx, &saved.Revision, &saved.Stage, &saved.Digest,
			&saved.Context, &saved.Edges, &saved.Schema, &saved.Calculator, &saved.DigestVersion); err != nil {
			return nil, fmt.Errorf("stage %d receipt is missing: %w", stage, err)
		}
		var snapshot MatchingFallbackCalculationContext
		if saved.Stage != stage || json.Unmarshal(saved.Context, &snapshot) != nil || snapshot.RunID != runID ||
			snapshot.AcquisitionRevision != saved.Revision || snapshot.StageIndex != stage {
			return nil, fmt.Errorf("stage %d receipt identity is invalid", stage)
		}
		var digest string
		switch {
		case saved.Schema == matchingFallbackCalculationSchemaVersion && saved.Calculator == matchingFallbackCalculationCalculatorVersion && saved.DigestVersion == matchingFallbackCalculationDigestVersion:
			digest, _ = MatchingFallbackCalculationInputDigest(snapshot)
		case stage == 0 && saved.Schema == matchingFallbackCalculationLegacySchemaVersion && saved.Calculator == matchingFallbackCalculationLegacyCalculatorVersion && saved.DigestVersion == matchingFallbackCalculationLegacyDigestVersion:
			digest, _ = MatchingFallbackCalculationInputDigestV2(snapshot)
		default:
			return nil, fmt.Errorf("stage %d receipt version is unsupported", stage)
		}
		if digest == "" || digest != saved.Digest {
			return nil, fmt.Errorf("stage %d receipt context digest is invalid", stage)
		}
		if err := ValidateMatchingFallbackCalculationEdges(snapshot, saved.Edges); err != nil {
			return nil, fmt.Errorf("stage %d receipt edges are invalid: %w", stage, err)
		}
		var edges []struct {
			Confidence float64 `json:"confidence"`
		}
		if json.Unmarshal(saved.Edges, &edges) != nil {
			return nil, fmt.Errorf("stage %d receipt edge list is invalid", stage)
		}
		for _, edge := range edges {
			if edge.Confidence >= 0.70 {
				return nil, fmt.Errorf("stage %d contains a qualifying edge", stage)
			}
		}
		history = append(history, fallbackApplicabilityHistoryReceipt{StageIndex: stage, Revision: saved.Revision, InputDigest: saved.Digest})
	}
	return history, nil
}
