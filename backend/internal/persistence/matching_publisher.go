package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublishMatchingEvaluationForDelivery atomically publishes evaluated results
// and completes the matching parent only for its registered current delivery.
// Delivery claiming/recovery and worker wiring intentionally remain separate.
func (repository *SetupManagerRepository) PublishMatchingEvaluationForDelivery(
	ctx context.Context,
	operationID uuid.UUID,
	attempt int,
	riverJobID int64,
	executionEpoch int64,
	evaluation IncomingMatchEvaluation,
	expected map[uuid.UUID]*string,
) (bool, error) {
	if expected == nil {
		return false, fmt.Errorf("publish matching evaluation: expected evaluation revisions are required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Acquire the output gate before reading or locking any source/output rows.
		// This preserves the admission lock order shared with publication.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("publish matching evaluation: lock output gate: %w", err)
		}
		if err := validateIncomingMatchEvaluation(evaluation); err != nil {
			return err
		}
		if err := validateMatchingPlanEvaluation(ctx, tx, operationID, evaluation); err != nil {
			return err
		}
		// The shared helper acquires source/group/provider/evaluation locks first.
		if err := replaceIncomingMatchesTx(ctx, tx, evaluation, expected); err != nil {
			return err
		}
		var targetIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=? ORDER BY group_id`, operationID).Scan(ctx, &targetIDs); err != nil {
			return fmt.Errorf("publish matching evaluation: read targets: %w", err)
		}
		if len(targetIDs) != len(evaluation.Groups) {
			return fmt.Errorf("publish matching evaluation: evaluated groups do not match the admitted targets")
		}
		generations := make([]matchingPublisherGeneration, 0, len(targetIDs))
		if err := tx.NewRaw(`SELECT generation.group_id, generation.high_water, generation.current_run_id,
			target.target_generation FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			ORDER BY generation.group_id FOR UPDATE OF generation`, operationID).Scan(ctx, &generations); err != nil {
			return fmt.Errorf("publish matching evaluation: lock target generations: %w", err)
		}
		if len(generations) != len(targetIDs) {
			return fmt.Errorf("publish matching evaluation: target generation is unavailable")
		}
		for i := range generations {
			if generations[i].GroupID != targetIDs[i] || generations[i].CurrentRunID == nil || *generations[i].CurrentRunID != operationID || generations[i].HighWater != generations[i].TargetGeneration {
				return fmt.Errorf("publish matching evaluation: admitted target was superseded")
			}
		}

		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.State != "running" || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return fmt.Errorf("publish matching evaluation: operation delivery is no longer current")
		}
		var run matchingPublisherRun
		if err := tx.NewRaw(`SELECT state, execution_epoch, current_river_job_id, active_river_job_id, active_execution_epoch
			FROM matching_run WHERE id=? AND operation_id=? FOR UPDATE`, operationID, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("publish matching evaluation: lock matching run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != executionEpoch || run.CurrentRiverJobID == nil || *run.CurrentRiverJobID != riverJobID || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID || run.ActiveExecutionEpoch == nil || *run.ActiveExecutionEpoch != executionEpoch {
			return fmt.Errorf("publish matching evaluation: matching execution is no longer current")
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, operationID, riverJobID, attempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("publish matching evaluation: validate registered delivery: %w", err)
		}
		if !registered {
			return fmt.Errorf("publish matching evaluation: delivery is not registered or was already consumed")
		}
		var unsettled int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, operationID).Scan(ctx, &unsettled); err != nil {
			return fmt.Errorf("publish matching evaluation: validate provider dependencies: %w", err)
		}
		if unsettled != 0 {
			return fmt.Errorf("publish matching evaluation: provider dependencies are still pending")
		}
		if repository.riverInserter != nil {
			if err := repository.enqueueEligibleMatchingApplies(ctx, tx, operationID, run.ExecutionEpoch, evaluation); err != nil {
				return fmt.Errorf("publish matching evaluation: admit automatic applies: %w", err)
			}
		}
		now := time.Now().UTC()
		operation.State, operation.Stage = "succeeded", "succeeded"
		operation.SafeError = nil
		operation.FinishedAt = &now
		operation.ProviderDeliveryClaimedAt = nil
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "provider_delivery_claimed_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("publish matching evaluation: complete operation: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE matching_run SET state='succeeded', active_river_job_id=NULL,
			active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=? AND execution_epoch=?`, now, operationID, executionEpoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("publish matching evaluation: complete matching run: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("publish matching evaluation: matching run completion was not applied")
		}
		result, err = tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND consumed_at IS NULL`, now, operationID, riverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("publish matching evaluation: consume delivery: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("publish matching evaluation: delivery consumption was not applied")
		}
		changed = true
		return nil
	})
	return changed, err
}

type matchingApplyIntent struct {
	SchemaVersion      int       `json:"schema_version"`
	ParentRunID        uuid.UUID `json:"parent_run_id"`
	GroupID            uuid.UUID `json:"group_id"`
	EvaluationRevision string    `json:"evaluation_revision"`
	ExecutionEpoch     int64     `json:"execution_epoch"`
}

func (repository *SetupManagerRepository) enqueueEligibleMatchingApplies(ctx context.Context, tx bun.Tx, runID uuid.UUID, executionEpoch int64, evaluation IncomingMatchEvaluation) error {
	var manualRun bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM manual_matching_admission WHERE run_id=?)`, runID).Scan(ctx, &manualRun); err != nil {
		return fmt.Errorf("check manual matching authority: %w", err)
	}
	if manualRun {
		return nil
	}
	for _, group := range evaluation.Groups {
		var header struct {
			Revision string          `bun:"evaluation_revision"`
			Evidence json.RawMessage `bun:"evaluation_evidence"`
		}
		if err := tx.NewRaw(`SELECT evaluation_revision, evaluation_evidence FROM incoming_match_evaluation WHERE group_id=?`, group.GroupID).Scan(ctx, &header); err != nil {
			return fmt.Errorf("read persisted decision for group %s: %w", group.GroupID, err)
		}
		outcome, err := decodeMatchingApplyOutcome(header.Evidence)
		if err != nil {
			return fmt.Errorf("decode persisted decision for group %s: %w", group.GroupID, err)
		}
		// Only existing uniquely qualified assignments are automatic. Review,
		// tie, and infeasible partial-layout decisions remain manual work.
		if outcome.GroupID != group.GroupID || outcome.Outcome != "unique" {
			continue
		}
		_, err = decodePersistedMatchingDecision(header.Evidence)
		if err != nil {
			return fmt.Errorf("decode unique persisted decision for group %s: %w", group.GroupID, err)
		}
		var alreadyRequested bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_apply_request WHERE group_id=? AND evaluation_revision=?)`, group.GroupID, header.Revision).Scan(ctx, &alreadyRequested); err != nil {
			return fmt.Errorf("check existing apply request: %w", err)
		}
		if alreadyRequested {
			continue
		}
		operationID := uuid.New()
		snapshot, err := json.Marshal(matchingApplyIntent{
			SchemaVersion: 1, ParentRunID: runID, GroupID: group.GroupID,
			EvaluationRevision: header.Revision, ExecutionEpoch: executionEpoch,
		})
		if err != nil {
			return fmt.Errorf("encode apply snapshot: %w", err)
		}
		operation := &Operation{ID: operationID, Kind: MatchingApplyOperationKind, State: "queued", Stage: "queued", InputSnapshot: snapshot}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, operation, repository.riverInserter, MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_apply_request(group_id,parent_run_id,evaluation_revision,apply_operation_id) VALUES (?,?,?,?)`, group.GroupID, runID, header.Revision, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("persist apply request: %w", err)
		}
	}
	return nil
}

type matchingPublisherGeneration struct {
	GroupID          uuid.UUID  `bun:"group_id"`
	HighWater        int64      `bun:"high_water"`
	CurrentRunID     *uuid.UUID `bun:"current_run_id"`
	TargetGeneration int64      `bun:"target_generation"`
}

type matchingPublisherRun struct {
	State                string `bun:"state"`
	ExecutionEpoch       int64  `bun:"execution_epoch"`
	CurrentRiverJobID    *int64 `bun:"current_river_job_id"`
	ActiveRiverJobID     *int64 `bun:"active_river_job_id"`
	ActiveExecutionEpoch *int64 `bun:"active_execution_epoch"`
}

type matchingPublisherPlan struct {
	SchemaVersion int                             `json:"schema_version"`
	GroupingEpoch int64                           `json:"grouping_epoch"`
	Targets       []MatchingTarget                `json:"targets"`
	PolicyVersion string                          `json:"policy_version"`
	Scope         matchingPublisherQueryScope     `json:"evaluation_query_scope"`
	Providers     []matchingPublisherPlanProvider `json:"provider_requests"`
}

type matchingPublisherQueryScope struct {
	GroupIDs           []uuid.UUID            `json:"group_ids"`
	ReleaseKeysByFile  map[uuid.UUID][]string `json:"release_keys_by_file"`
	AcoustIDKeysByFile map[uuid.UUID]string   `json:"acoustid_keys_by_file,omitempty"`
	SearchKeysByFile   map[uuid.UUID]struct {
		Release   string `json:"release"`
		Recording string `json:"recording"`
	} `json:"search_keys_by_file"`
}

// GetMatchingAcquisitionEvaluationSeal returns the immutable evaluator handoff
// only while its acquisition ledger still points at the sealing phase. The
// digest and accepted-catalog manifest are rechecked on every read.
func (repository *SetupManagerRepository) GetMatchingAcquisitionEvaluationSeal(ctx context.Context, runID uuid.UUID) (MatchingAcquisitionEvaluationInput, bool, error) {
	var input MatchingAcquisitionEvaluationInput
	var exists bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(ctx context.Context, tx bun.Tx) error {
		var row struct {
			Input           []byte `bun:"input"`
			InputDigest     []byte `bun:"input_digest"`
			CatalogDigest   []byte `bun:"catalog_digest"`
			SealedRevision  int64  `bun:"acquisition_revision"`
			CurrentRevision int64  `bun:"revision"`
			CurrentPhase    string `bun:"current_phase"`
			PhaseName       string `bun:"phase"`
		}
		err := tx.NewRaw(`SELECT seal.input, seal.input_digest, seal.catalog_digest,
			seal.acquisition_revision, acquisition.revision, acquisition.current_phase, phase.phase
			FROM matching_acquisition_seal seal
			JOIN matching_acquisition acquisition ON acquisition.run_id=seal.run_id
			JOIN matching_acquisition_phase phase ON phase.run_id=seal.run_id AND phase.revision=acquisition.revision
			WHERE seal.run_id=?`, runID).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read matching acquisition evaluation seal: %w", err)
		}
		if row.CurrentPhase != "evaluation_sealed" || row.PhaseName != "evaluation_sealed" || row.SealedRevision != row.CurrentRevision {
			return fmt.Errorf("read matching acquisition evaluation seal: ledger phase does not match immutable seal")
		}
		if err := json.Unmarshal(row.Input, &input); err != nil {
			return fmt.Errorf("read matching acquisition evaluation seal: decode input: %w", err)
		}
		_, _, digest, err := canonicalMatchingAcquisitionInput(input)
		if err != nil || !equalDigest(row.InputDigest, digest[:]) {
			return fmt.Errorf("read matching acquisition evaluation seal: input digest or encoding is invalid")
		}
		catalogDigest, _, err := matchingAcquisitionCatalogManifest(ctx, tx, runID)
		if err != nil {
			return fmt.Errorf("read matching acquisition evaluation seal: accepted catalog manifest: %w", err)
		}
		if !equalDigest(row.CatalogDigest, catalogDigest[:]) {
			return fmt.Errorf("read matching acquisition evaluation seal: accepted catalog manifest changed")
		}
		if input.SchemaVersion == 2 {
			if validateMatchingCatalogCacheKeys(ctx, tx, runID, input.CatalogCacheKeys) != nil || row.SealedRevision < 2 ||
				validateMatchingReleaseHydration(ctx, tx, runID, row.SealedRevision-1, input.ReleaseIDs, input.ReleaseLookupCacheKeys, input.ReleaseLookupPayloadSHA256) != nil {
				return fmt.Errorf("read matching acquisition evaluation seal: accepted hydration manifest changed")
			}
		}
		exists = true
		return nil
	})
	if err != nil {
		return MatchingAcquisitionEvaluationInput{}, false, err
	}
	return input, exists, nil
}

type matchingPublisherPlanProvider struct {
	ProviderSourceID              uuid.UUID `json:"provider_source_id"`
	EndpointConfigurationIdentity string    `json:"endpoint_configuration_identity"`
	ProviderID                    uuid.UUID `json:"provider_id"`
}

type matchingPublisherRunPlan struct {
	Plan                  json.RawMessage `bun:"immutable_plan"`
	GroupingEpoch         int64           `bun:"grouping_epoch"`
	PolicyVersion         string          `bun:"policy_version"`
	ProviderSourceID      uuid.UUID       `bun:"provider_source_id"`
	ConfigurationIdentity string          `bun:"provider_configuration_identity"`
}

func validateMatchingPlanEvaluation(ctx context.Context, tx bun.Tx, runID uuid.UUID, evaluation IncomingMatchEvaluation) error {
	var run matchingPublisherRunPlan
	if err := tx.NewRaw(`SELECT immutable_plan, grouping_epoch, policy_version, provider_source_id, provider_configuration_identity
		FROM matching_run WHERE id=?`, runID).Scan(ctx, &run); err != nil {
		return fmt.Errorf("publish matching evaluation: load admitted plan: %w", err)
	}
	var planEnvelope struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(run.Plan, &planEnvelope) == nil && planEnvelope.Mode == "manual_review" {
		return validateManualMatchingPlanEvaluation(ctx, tx, runID, run, evaluation)
	}
	var plan matchingPublisherPlan
	if err := json.Unmarshal(run.Plan, &plan); err != nil {
		return fmt.Errorf("publish matching evaluation: decode admitted plan: %w", err)
	}
	if plan.SchemaVersion != 2 && plan.SchemaVersion != 3 {
		return fmt.Errorf("publish matching evaluation: admitted plan schema version is unsupported")
	}
	if plan.SchemaVersion == 2 && len(plan.Scope.AcoustIDKeysByFile) != 0 || plan.SchemaVersion == 3 && len(plan.Scope.AcoustIDKeysByFile) == 0 {
		return fmt.Errorf("publish matching evaluation: admitted plan version does not match its AcoustID scope")
	}
	if plan.GroupingEpoch != run.GroupingEpoch || evaluation.ExpectedGroupingEpoch != run.GroupingEpoch || plan.PolicyVersion != run.PolicyVersion || evaluation.PolicyVersion != run.PolicyVersion || len(plan.Targets) == 0 || len(plan.Scope.GroupIDs) != len(plan.Targets) {
		return fmt.Errorf("publish matching evaluation: evaluation does not match admitted plan")
	}
	targets := append([]MatchingTarget(nil), plan.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].GroupID.String() < targets[j].GroupID.String() })
	groupIDs := append([]uuid.UUID(nil), plan.Scope.GroupIDs...)
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i].String() < groupIDs[j].String() })
	if len(targets) != len(evaluation.Groups) || len(groupIDs) != len(targets) {
		return fmt.Errorf("publish matching evaluation: evaluated groups do not match admitted plan")
	}
	evaluated := make(map[uuid.UUID]IncomingMatchGroupEvaluation, len(evaluation.Groups))
	for _, group := range evaluation.Groups {
		evaluated[group.GroupID] = group
	}
	for i, target := range targets {
		group, exists := evaluated[target.GroupID]
		if !exists || target.GroupID == uuid.Nil || target.Revision == "" || target.GroupID != groupIDs[i] || target.Revision != group.GroupRevision {
			return fmt.Errorf("publish matching evaluation: evaluated group differs from admitted target")
		}
	}
	if evaluation.ScopeVersion == 4 {
		if err := validateSealedMatchingEvaluation(ctx, tx, runID, evaluation); err != nil {
			return err
		}
	} else if err := validateMatchingPlanQueryScope(plan.Scope, evaluation); err != nil {
		return err
	}
	if evaluation.Provider.ProviderSourceID != run.ProviderSourceID || evaluation.Provider.ConfigurationIdentity != run.ConfigurationIdentity {
		return fmt.Errorf("publish matching evaluation: provider identity differs from admitted plan")
	}
	for _, provider := range plan.Providers {
		if provider.ProviderSourceID != run.ProviderSourceID || provider.EndpointConfigurationIdentity != run.ConfigurationIdentity || provider.ProviderID != evaluation.Provider.ProviderID {
			return fmt.Errorf("publish matching evaluation: provider request differs from admitted plan")
		}
	}
	if len(plan.Providers) == 0 {
		return fmt.Errorf("publish matching evaluation: admitted provider request is missing")
	}
	return nil
}

func validateManualMatchingPlanEvaluation(ctx context.Context, tx bun.Tx, runID uuid.UUID, run matchingPublisherRunPlan, evaluation IncomingMatchEvaluation) error {
	var plan struct {
		SchemaVersion int             `json:"schema_version"`
		Mode          string          `json:"mode"`
		RequestID     uuid.UUID       `json:"request_id"`
		GroupID       uuid.UUID       `json:"group_id"`
		GroupRevision string          `json:"group_revision"`
		Evaluation    *string         `json:"evaluation_revision"`
		ReleaseMBID   uuid.UUID       `json:"selected_release_mbid"`
		Plan          json.RawMessage `json:"plan"`
		GroupContext  json.RawMessage `json:"group_context"`
		SourceContext json.RawMessage `json:"source_context"`
		LookupID      uuid.UUID       `json:"lookup_operation_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(run.Plan))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || decoder.Decode(new(any)) != io.EOF || plan.SchemaVersion != 1 || plan.Mode != "manual_review" || plan.RequestID == uuid.Nil || plan.GroupID == uuid.Nil || plan.GroupRevision == "" || plan.Evaluation != nil && *plan.Evaluation == "" || plan.ReleaseMBID == uuid.Nil || plan.LookupID == uuid.Nil || !jsonObject(plan.Plan) || !jsonObject(plan.GroupContext) || !jsonObject(plan.SourceContext) {
		return fmt.Errorf("publish matching evaluation: manual plan is invalid")
	}
	if evaluation.ScopeVersion != 4 || evaluation.ExpectedGroupingEpoch != run.GroupingEpoch || evaluation.PolicyVersion != run.PolicyVersion || len(evaluation.Groups) != 1 || evaluation.Groups[0].GroupID != plan.GroupID || evaluation.Groups[0].GroupRevision != plan.GroupRevision || evaluation.Provider.ProviderSourceID != run.ProviderSourceID || evaluation.Provider.ConfigurationIdentity != run.ConfigurationIdentity {
		return fmt.Errorf("publish matching evaluation: manual evaluation differs from admitted scope")
	}
	var capturedSource struct {
		ProviderID            uuid.UUID `json:"provider_id"`
		ProviderSourceID      uuid.UUID `json:"provider_source_id"`
		Endpoint              string    `json:"endpoint"`
		ConfigurationIdentity string    `json:"configuration_identity"`
	}
	if err := json.Unmarshal(plan.SourceContext, &capturedSource); err != nil || capturedSource.ProviderID == uuid.Nil || capturedSource.ProviderSourceID != run.ProviderSourceID || capturedSource.Endpoint == "" || capturedSource.ConfigurationIdentity != run.ConfigurationIdentity {
		return fmt.Errorf("publish matching evaluation: captured manual provider scope is invalid")
	}
	var providerSource struct {
		ProviderID            uuid.UUID `bun:"provider_id"`
		Endpoint              string    `bun:"endpoint"`
		ConfigurationIdentity string    `bun:"configuration_identity"`
	}
	if err := tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, capturedSource.ProviderSourceID).Scan(ctx, &providerSource); err != nil || providerSource.ProviderID != capturedSource.ProviderID || providerSource.Endpoint != capturedSource.Endpoint || providerSource.ConfigurationIdentity != capturedSource.ConfigurationIdentity || evaluation.Provider.ProviderID != capturedSource.ProviderID || evaluation.Provider.Endpoint != capturedSource.Endpoint {
		return fmt.Errorf("publish matching evaluation: captured manual provider source changed")
	}
	var admission struct {
		RunID              uuid.UUID       `bun:"run_id"`
		GroupID            uuid.UUID       `bun:"group_id"`
		GroupRevision      string          `bun:"group_revision"`
		EvaluationRevision *string         `bun:"evaluation_revision"`
		ReleaseMBID        uuid.UUID       `bun:"selected_release_mbid"`
		LookupID           uuid.UUID       `bun:"lookup_operation_id"`
		CacheKey           string          `bun:"cache_key"`
		Members            json.RawMessage `bun:"original_members"`
	}
	if err := tx.NewRaw(`SELECT run_id,group_id,group_revision,evaluation_revision,selected_release_mbid,lookup_operation_id,cache_key,original_members FROM manual_matching_admission WHERE request_id=? FOR SHARE`, plan.RequestID).Scan(ctx, &admission); err != nil {
		return fmt.Errorf("publish matching evaluation: read manual authority: %w", err)
	}
	if admission.RunID != runID || admission.GroupID != plan.GroupID || admission.GroupRevision != plan.GroupRevision || !equalOptionalString(admission.EvaluationRevision, plan.Evaluation) || admission.ReleaseMBID != plan.ReleaseMBID || admission.LookupID != plan.LookupID || admission.CacheKey != "release-lookup:"+strings.ToLower(plan.ReleaseMBID.String()) {
		return fmt.Errorf("publish matching evaluation: manual plan differs from immutable admission")
	}
	var authority struct {
		Version             int       `json:"version"`
		ExecutionMode       string    `json:"execution_mode"`
		SelectedReleaseMBID uuid.UUID `json:"selected_release_mbid"`
	}
	var evidence map[string]json.RawMessage
	if json.Unmarshal(evaluation.Groups[0].Evidence, &evidence) != nil {
		return fmt.Errorf("publish matching evaluation: manual evidence is invalid")
	}
	rawAuthority, exists := evidence["manual_authority"]
	if !exists {
		return fmt.Errorf("publish matching evaluation: manual authority evidence is missing")
	}
	decoder = json.NewDecoder(bytes.NewReader(rawAuthority))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil || decoder.Decode(new(any)) != io.EOF || authority.Version != 1 || authority.ExecutionMode != "manual_review" || authority.SelectedReleaseMBID != plan.ReleaseMBID {
		return fmt.Errorf("publish matching evaluation: manual authority evidence differs from admission")
	}
	var lookupEvidence struct {
		RequestID         uuid.UUID `json:"request_id"`
		GroupID           uuid.UUID `json:"group_id"`
		LookupOperationID uuid.UUID `json:"lookup_operation_id"`
		PayloadSHA256     string    `json:"payload_sha256"`
	}
	rawLookup, exists := evidence["manual_lookup"]
	if !exists {
		return fmt.Errorf("publish matching evaluation: accepted manual lookup evidence is missing")
	}
	decoder = json.NewDecoder(bytes.NewReader(rawLookup))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lookupEvidence); err != nil || decoder.Decode(new(any)) != io.EOF || lookupEvidence.RequestID != plan.RequestID || lookupEvidence.GroupID != plan.GroupID || lookupEvidence.LookupOperationID != plan.LookupID || len(lookupEvidence.PayloadSHA256) != 64 {
		return fmt.Errorf("publish matching evaluation: accepted manual lookup evidence differs from admission")
	}
	var lookupSeal struct {
		RequestID             uuid.UUID `bun:"request_id"`
		LookupID              uuid.UUID `bun:"lookup_operation_id"`
		ProviderSourceID      uuid.UUID `bun:"provider_source_id"`
		ConfigurationIdentity string    `bun:"configuration_identity"`
		CacheKey              string    `bun:"cache_key"`
		PayloadSHA256         []byte    `bun:"payload_sha256"`
		CacheRevision         *string   `bun:"cache_revision"`
		ProjectionSHA256      []byte    `bun:"projection_sha256"`
	}
	if err := tx.NewRaw(`SELECT seal.request_id,seal.lookup_operation_id,seal.provider_source_id,seal.configuration_identity,
		seal.cache_key,seal.payload_sha256,seal.cache_revision,seal.projection_sha256
		FROM manual_matching_lookup_seal seal
		JOIN manual_matching_admission admission ON admission.request_id=seal.request_id
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=admission.run_id AND dependency.request_id=seal.lookup_operation_id AND dependency.cache_key=seal.cache_key
		JOIN operation lookup ON lookup.id=seal.lookup_operation_id
		WHERE seal.request_id=? AND seal.lookup_operation_id=? AND seal.sealed_at IS NOT NULL
		AND dependency.terminal_outcome='succeeded' AND lookup.state='succeeded'
		FOR SHARE OF seal,dependency,lookup`, plan.RequestID, plan.LookupID).Scan(ctx, &lookupSeal); err != nil {
		return fmt.Errorf("publish matching evaluation: read sealed manual lookup: %w", err)
	}
	evidencePayloadDigest := mustDecodeSHA256(lookupEvidence.PayloadSHA256)
	evaluationProjectionDigest := mustDecodeSHA256(evaluation.Provider.AcceptedProjectionRevision)
	if lookupSeal.RequestID != plan.RequestID || lookupSeal.LookupID != plan.LookupID || lookupSeal.ProviderSourceID != capturedSource.ProviderSourceID ||
		lookupSeal.ConfigurationIdentity != capturedSource.ConfigurationIdentity || lookupSeal.CacheKey != admission.CacheKey ||
		len(lookupSeal.PayloadSHA256) != sha256.Size || len(lookupSeal.ProjectionSHA256) != sha256.Size ||
		len(evidencePayloadDigest) != sha256.Size || len(evaluationProjectionDigest) != sha256.Size ||
		!equalDigest(lookupSeal.PayloadSHA256, evidencePayloadDigest) || !equalDigest(lookupSeal.ProjectionSHA256, evaluationProjectionDigest) {
		return fmt.Errorf("publish matching evaluation: manual lookup does not match its immutable seal")
	}
	cacheRevisions := effectiveMatchCacheRevisions(evaluation.Provider)
	cacheRevision, hasCacheRevision := cacheRevisions[lookupSeal.CacheKey]
	if len(cacheRevisions) != 1 || !hasCacheRevision || !equalOptionalString(cacheRevision, lookupSeal.CacheRevision) || len(evaluation.Provider.ReleaseKeys) != 1 || evaluation.Provider.ReleaseKeys[0] != plan.ReleaseMBID.String() {
		return fmt.Errorf("publish matching evaluation: manual provider scope differs from sealed lookup")
	}
	var accepted struct {
		Payload []byte `bun:"payload"`
		Outcome string `bun:"terminal_outcome"`
	}
	if err := tx.NewRaw(`SELECT cache.payload,dependency.terminal_outcome FROM matching_provider_dependency dependency JOIN matching_run run ON run.id=dependency.parent_run_id JOIN provider_response_cache cache ON cache.provider_source_id=run.provider_source_id AND cache.cache_key=dependency.cache_key WHERE dependency.parent_run_id=? AND dependency.request_id=? AND dependency.terminal_outcome='succeeded' FOR SHARE OF dependency,cache`, runID, plan.LookupID).Scan(ctx, &accepted); err != nil {
		return fmt.Errorf("publish matching evaluation: accepted manual lookup is unavailable: %w", err)
	}
	sum := sha256.Sum256(accepted.Payload)
	if hex.EncodeToString(sum[:]) != strings.ToLower(lookupEvidence.PayloadSHA256) {
		return fmt.Errorf("publish matching evaluation: accepted manual lookup changed")
	}
	var captured []IncomingMatchMemberFence
	if json.Unmarshal(admission.Members, &captured) != nil || !sameManualMembers(captured, evaluation.Groups[0].Members) {
		return fmt.Errorf("publish matching evaluation: captured manual member scope changed")
	}
	return nil
}

func mustDecodeSHA256(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil
	}
	return decoded
}

func sameManualMembers(left, right []IncomingMatchMemberFence) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].FileMemberID != right[i].FileMemberID || left[i].WorkID != right[i].WorkID || !reflect.DeepEqual(left[i].SourceSHA256, right[i].SourceSHA256) {
			return false
		}
	}
	return true
}

func validateSealedMatchingEvaluation(ctx context.Context, tx bun.Tx, runID uuid.UUID, evaluation IncomingMatchEvaluation) error {
	var row struct {
		Input         []byte `bun:"input"`
		InputDigest   []byte `bun:"input_digest"`
		CatalogDigest []byte `bun:"catalog_digest"`
		Revision      int64  `bun:"acquisition_revision"`
		Current       int64  `bun:"revision"`
		Phase         string `bun:"current_phase"`
		PhaseName     string `bun:"phase"`
	}
	if err := tx.NewRaw(`SELECT seal.input, seal.input_digest, seal.catalog_digest, seal.acquisition_revision,
		acquisition.revision, acquisition.current_phase, phase.phase
		FROM matching_acquisition_seal seal
		JOIN matching_acquisition acquisition ON acquisition.run_id=seal.run_id
		JOIN matching_acquisition_phase phase ON phase.run_id=seal.run_id AND phase.revision=acquisition.revision
		WHERE seal.run_id=? FOR SHARE OF seal, acquisition, phase`, runID).Scan(ctx, &row); err != nil {
		return fmt.Errorf("publish matching evaluation: read sealed catalog scope: %w", err)
	}
	if row.Phase != "evaluation_sealed" || row.PhaseName != row.Phase || row.Revision != row.Current {
		return fmt.Errorf("publish matching evaluation: acquisition seal is not current")
	}
	var input MatchingAcquisitionEvaluationInput
	if err := json.Unmarshal(row.Input, &input); err != nil {
		return fmt.Errorf("publish matching evaluation: decode sealed scope: %w", err)
	}
	_, _, digest, err := canonicalMatchingAcquisitionInput(input)
	if err != nil || !equalDigest(row.InputDigest, digest[:]) {
		return fmt.Errorf("publish matching evaluation: sealed scope digest is invalid")
	}
	catalogDigest, acceptedIDs, err := matchingAcquisitionCatalogManifest(ctx, tx, runID)
	if err != nil {
		return fmt.Errorf("publish matching evaluation: read accepted catalog manifest: %w", err)
	}
	if !equalDigest(row.CatalogDigest, catalogDigest[:]) {
		return fmt.Errorf("publish matching evaluation: accepted catalog manifest changed")
	}
	if len(input.ReleaseIDs) == 0 || len(input.ReleaseIDs) > 10 || len(evaluation.Provider.ReleaseKeys) != len(input.ReleaseIDs) {
		return fmt.Errorf("publish matching evaluation: evaluated releases differ from sealed selection")
	}
	for index, releaseID := range input.ReleaseIDs {
		if _, ok := acceptedIDs[releaseID]; !ok || evaluation.Provider.ReleaseKeys[index] != releaseID {
			return fmt.Errorf("publish matching evaluation: evaluated release is outside sealed catalog scope")
		}
	}
	memberIDs := make(map[uuid.UUID]bool)
	if len(input.GroupIDs) != len(evaluation.Groups) {
		return fmt.Errorf("publish matching evaluation: evaluated groups differ from sealed scope")
	}
	evaluatedGroups := make(map[uuid.UUID]bool, len(evaluation.Groups))
	for _, group := range evaluation.Groups {
		evaluatedGroups[group.GroupID] = true
		for _, member := range group.Members {
			memberIDs[member.FileMemberID] = true
		}
	}
	for _, groupID := range input.GroupIDs {
		if !evaluatedGroups[groupID] {
			return fmt.Errorf("publish matching evaluation: evaluated group is outside sealed scope")
		}
	}
	if input.SchemaVersion == 1 && len(input.EvidenceCacheKeysByFile) != len(memberIDs) || input.SchemaVersion == 2 && len(input.EvidenceCacheKeysByFile) != 0 {
		return fmt.Errorf("publish matching evaluation: sealed response provenance does not cover evaluated files")
	}
	var provenanceRows []struct {
		ResponseProvenance []byte `bun:"response_provenance"`
	}
	if err := tx.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase
		WHERE run_id=? AND phase IN ('artist_catalog_page','catalog_ready') ORDER BY revision`, runID).Scan(ctx, &provenanceRows); err != nil {
		return fmt.Errorf("publish matching evaluation: read accepted response provenance: %w", err)
	}
	acceptedCacheKeys := make(map[string]bool)
	for _, row := range provenanceRows {
		var provenance []matchingAcquisitionResponseProvenance
		if err := json.Unmarshal(row.ResponseProvenance, &provenance); err != nil || len(provenance) != 1 || provenance[0].CacheKey == "" {
			return fmt.Errorf("publish matching evaluation: accepted response provenance is invalid")
		}
		acceptedCacheKeys[provenance[0].CacheKey] = true
	}
	if input.SchemaVersion == 2 {
		if validateMatchingCatalogCacheKeys(ctx, tx, runID, input.CatalogCacheKeys) != nil || row.Revision < 2 ||
			validateMatchingReleaseHydration(ctx, tx, runID, row.Revision-1, input.ReleaseIDs, input.ReleaseLookupCacheKeys, input.ReleaseLookupPayloadSHA256) != nil {
			return fmt.Errorf("publish matching evaluation: accepted hydration provenance is invalid")
		}
		for _, key := range input.ReleaseLookupCacheKeys {
			acceptedCacheKeys[key] = true
		}
	}
	if len(acceptedCacheKeys) == 0 {
		return fmt.Errorf("publish matching evaluation: accepted response provenance is empty")
	}
	expectedCacheKeys := make(map[string]bool, len(acceptedCacheKeys))
	for key := range acceptedCacheKeys {
		expectedCacheKeys[key] = true
	}
	for memberID, keys := range input.EvidenceCacheKeysByFile {
		if !memberIDs[memberID] || len(keys) == 0 {
			return fmt.Errorf("publish matching evaluation: sealed response provenance contains an invalid member")
		}
		if len(keys) != len(acceptedCacheKeys) {
			return fmt.Errorf("publish matching evaluation: sealed response provenance differs from accepted catalog pages")
		}
		seenKeys := make(map[string]bool, len(keys))
		for _, key := range keys {
			if !acceptedCacheKeys[key] || seenKeys[key] {
				return fmt.Errorf("publish matching evaluation: sealed response key is not an accepted catalog page")
			}
			seenKeys[key] = true
		}
	}
	if len(expectedCacheKeys) != len(evaluation.Provider.CacheRevisions) {
		return fmt.Errorf("publish matching evaluation: accepted response fences differ from sealed provenance")
	}
	for key := range expectedCacheKeys {
		if _, exists := evaluation.Provider.CacheRevisions[key]; !exists {
			return fmt.Errorf("publish matching evaluation: sealed accepted response fence is missing")
		}
	}
	var releaseIDs []uuid.UUID
	if len(input.ReleaseIDs) > 0 {
		if err := tx.NewRaw(`SELECT release.id FROM provider_release release JOIN provider_source_entity entity
			ON entity.provider_id=release.provider_id AND entity.provider_key=release.provider_key
			WHERE entity.provider_source_id=? AND entity.provider_id=? AND entity.entity_kind='release' AND entity.provider_key IN (?)
			ORDER BY entity.provider_key`, evaluation.Provider.ProviderSourceID, evaluation.Provider.ProviderID, bun.List(input.ReleaseIDs)).Scan(ctx, &releaseIDs); err != nil {
			return fmt.Errorf("publish matching evaluation: resolve selected release graph: %w", err)
		}
	}
	selected := make(map[uuid.UUID]bool, len(releaseIDs))
	for _, id := range releaseIDs {
		selected[id] = true
	}
	for _, group := range evaluation.Groups {
		for _, candidate := range group.Candidates {
			if !selected[candidate.ReleaseID] {
				return fmt.Errorf("publish matching evaluation: candidate is outside sealed release selection")
			}
		}
	}
	return nil
}

func validateMatchingPlanQueryScope(scope matchingPublisherQueryScope, evaluation IncomingMatchEvaluation) error {
	memberGroups := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(evaluation.Groups))
	expectedReleaseKeys := make(map[string]struct{})
	expectedQueryKeys := make(map[string]struct{})
	selectedMembers := 0
	for _, group := range evaluation.Groups {
		members := make(map[uuid.UUID]struct{}, len(group.Members))
		for _, member := range group.Members {
			members[member.FileMemberID] = struct{}{}
		}
		memberGroups[group.GroupID] = members
		selectedMembers += len(members)
	}
	if len(scope.ReleaseKeysByFile) != selectedMembers || len(scope.SearchKeysByFile) != selectedMembers {
		return fmt.Errorf("publish matching evaluation: admitted query scope does not cover exactly the evaluated members")
	}
	for memberID, key := range scope.AcoustIDKeysByFile {
		if key == "" || !matchingQueryScopeContainsMember(memberGroups, memberID) {
			return fmt.Errorf("publish matching evaluation: admitted AcoustID scope contains an invalid member or key")
		}
	}
	if len(evaluation.Provider.AcoustIDSources) != len(scope.AcoustIDKeysByFile) {
		return fmt.Errorf("publish matching evaluation: AcoustID source fences do not cover exactly the admitted scope")
	}
	sourceByMember := make(map[uuid.UUID]IncomingMatchAcoustIDSourceFence, len(evaluation.Provider.AcoustIDSources))
	for _, source := range evaluation.Provider.AcoustIDSources {
		key, scoped := scope.AcoustIDKeysByFile[source.FileMemberID]
		if !scoped || source.CacheKey != key {
			return fmt.Errorf("publish matching evaluation: AcoustID source fence is outside admitted scope")
		}
		if _, duplicate := sourceByMember[source.FileMemberID]; duplicate {
			return fmt.Errorf("publish matching evaluation: AcoustID source fence repeats a member")
		}
		sourceByMember[source.FileMemberID] = source
	}
	for memberID, releases := range scope.ReleaseKeysByFile {
		keys, ok := scope.SearchKeysByFile[memberID]
		if !ok || keys.Release == "" || keys.Recording == "" {
			return fmt.Errorf("publish matching evaluation: admitted search identity is missing")
		}
		for index, key := range releases {
			if key == "" || index > 0 && releases[index-1] >= key {
				return fmt.Errorf("publish matching evaluation: admitted release identities are not canonical")
			}
			expectedReleaseKeys[key] = struct{}{}
		}
		expectedQueryKeys[keys.Release] = struct{}{}
		expectedQueryKeys[keys.Recording] = struct{}{}
	}
	for memberID := range scope.SearchKeysByFile {
		if !matchingQueryScopeContainsMember(memberGroups, memberID) {
			return fmt.Errorf("publish matching evaluation: admitted query scope contains an unselected member")
		}
	}
	for _, group := range evaluation.Groups {
		var evidence struct {
			Files []struct {
				FileMemberID      uuid.UUID       `json:"file_member_id"`
				ReleaseQueryKey   string          `json:"release_query_key"`
				RecordingQueryKey string          `json:"recording_query_key"`
				AcoustIDEvidence  json.RawMessage `json:"acoustid_cache_evidence"`
			} `json:"files"`
		}
		if err := json.Unmarshal(group.Evidence, &evidence); err != nil || len(evidence.Files) != len(group.Members) {
			return fmt.Errorf("publish matching evaluation: group evidence does not verify the admitted query scope")
		}
		seen := make(map[uuid.UUID]struct{}, len(evidence.Files))
		groupMembers := memberGroups[group.GroupID]
		for _, file := range evidence.Files {
			if _, belongsToGroup := groupMembers[file.FileMemberID]; !belongsToGroup {
				return fmt.Errorf("publish matching evaluation: group evidence contains a member from another group")
			}
			keys, ok := scope.SearchKeysByFile[file.FileMemberID]
			if !ok || keys.Release == "" || keys.Recording == "" || keys.Release != file.ReleaseQueryKey || keys.Recording != file.RecordingQueryKey {
				return fmt.Errorf("publish matching evaluation: evaluated query identity differs from admitted scope")
			}
			if _, duplicate := seen[file.FileMemberID]; duplicate {
				return fmt.Errorf("publish matching evaluation: group evidence repeats a member")
			}
			acoustIDKey, scoped := scope.AcoustIDKeysByFile[file.FileMemberID]
			if scoped && len(file.AcoustIDEvidence) == 0 {
				return fmt.Errorf("publish matching evaluation: scoped AcoustID evidence field is missing")
			}
			if !scoped && len(file.AcoustIDEvidence) != 0 && string(file.AcoustIDEvidence) != "null" {
				return fmt.Errorf("publish matching evaluation: AcoustID evidence is outside admitted scope")
			}
			if scoped && len(file.AcoustIDEvidence) != 0 && string(file.AcoustIDEvidence) != "null" {
				var actual struct {
					CacheKey         string `json:"cache_key"`
					AcceptedRevision string `json:"accepted_revision"`
					Diagnostic       string `json:"diagnostic"`
				}
				if err := json.Unmarshal(file.AcoustIDEvidence, &actual); err != nil {
					return fmt.Errorf("publish matching evaluation: AcoustID evidence differs from admitted key")
				}
				if actual.Diagnostic != "" {
					if actual.CacheKey != "" || actual.AcceptedRevision != "" {
						return fmt.Errorf("publish matching evaluation: diagnostic AcoustID evidence contains a score reference")
					}
					for _, fence := range evaluation.Provider.AcoustID {
						if fence.FileMemberID == file.FileMemberID {
							return fmt.Errorf("publish matching evaluation: diagnostic AcoustID evidence has a response fence")
						}
					}
					if _, ok := sourceByMember[file.FileMemberID]; !ok {
						return fmt.Errorf("publish matching evaluation: diagnostic AcoustID evidence lacks a source fence")
					}
					seen[file.FileMemberID] = struct{}{}
					continue
				}
				if actual.CacheKey != acoustIDKey || actual.AcceptedRevision == "" {
					return fmt.Errorf("publish matching evaluation: AcoustID evidence differs from admitted key")
				}
				source, ok := sourceByMember[file.FileMemberID]
				if !ok || source.ExpectedAcceptedRevision == nil || *source.ExpectedAcceptedRevision != actual.AcceptedRevision {
					return fmt.Errorf("publish matching evaluation: AcoustID evidence differs from source revision fence")
				}
				fenced := false
				for _, fence := range evaluation.Provider.AcoustID {
					if fence.FileMemberID == file.FileMemberID && fence.CacheKey == acoustIDKey && fence.AcceptedRevision == actual.AcceptedRevision {
						fenced = true
					}
				}
				if !fenced {
					return fmt.Errorf("publish matching evaluation: AcoustID evidence is not fenced by accepted response")
				}
			}
			if scoped && string(file.AcoustIDEvidence) == "null" {
				source, ok := sourceByMember[file.FileMemberID]
				if !ok || source.ExpectedAcceptedRevision != nil {
					return fmt.Errorf("publish matching evaluation: absent AcoustID evidence disagrees with source revision fence")
				}
				for _, fence := range evaluation.Provider.AcoustID {
					if fence.FileMemberID == file.FileMemberID {
						return fmt.Errorf("publish matching evaluation: absent AcoustID evidence has an unexpected cache fence")
					}
				}
			}
			seen[file.FileMemberID] = struct{}{}
		}
		if len(seen) != len(memberGroups[group.GroupID]) {
			return fmt.Errorf("publish matching evaluation: group evidence omits an evaluated member")
		}
	}
	for _, fence := range evaluation.Provider.AcoustID {
		if key, ok := scope.AcoustIDKeysByFile[fence.FileMemberID]; !ok || key != fence.CacheKey {
			return fmt.Errorf("publish matching evaluation: AcoustID response fence is outside admitted scope")
		}
	}
	for _, fence := range evaluation.Provider.AcoustIDSources {
		if fence.ExpectedAcceptedRevision == nil {
			continue
		}
		found := false
		for _, response := range evaluation.Provider.AcoustID {
			if response.FileMemberID == fence.FileMemberID && response.CacheKey == fence.CacheKey && response.AcceptedRevision == *fence.ExpectedAcceptedRevision {
				found = true
				break
			}
		}
		// Diagnostics intentionally do not apply the accepted response to scoring.
		if !found {
			for _, group := range evaluation.Groups {
				var evidence struct {
					Files []struct {
						FileMemberID uuid.UUID `json:"file_member_id"`
						Evidence     struct {
							Diagnostic string `json:"diagnostic"`
						} `json:"acoustid_cache_evidence"`
					} `json:"files"`
				}
				_ = json.Unmarshal(group.Evidence, &evidence)
				for _, file := range evidence.Files {
					if file.FileMemberID == fence.FileMemberID && file.Evidence.Diagnostic != "" {
						found = true
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("publish matching evaluation: source revision lacks matching response evidence")
		}
	}
	queryRevisions := effectiveMatchCacheRevisions(evaluation.Provider)
	if len(queryRevisions) != len(expectedQueryKeys) {
		return fmt.Errorf("publish matching evaluation: cached query revisions differ from admitted search scope")
	}
	for key := range expectedQueryKeys {
		if _, ok := queryRevisions[key]; !ok {
			return fmt.Errorf("publish matching evaluation: cached query revisions omit an admitted search key")
		}
	}
	if len(expectedReleaseKeys) != len(evaluation.Provider.ReleaseKeys) {
		return fmt.Errorf("publish matching evaluation: inspected releases differ from admitted scope")
	}
	for _, key := range evaluation.Provider.ReleaseKeys {
		if _, ok := expectedReleaseKeys[key]; !ok {
			return fmt.Errorf("publish matching evaluation: inspected release differs from admitted scope")
		}
	}
	return nil
}

// effectiveMatchCacheRevisions gives validation and locking one canonical view
// of both multi-query fences and legacy single-key fences.
func effectiveMatchCacheRevisions(fence IncomingMatchProviderFence) map[string]*string {
	if len(fence.CacheRevisions) != 0 {
		return fence.CacheRevisions
	}
	return map[string]*string{fence.CacheKey: fence.CacheRevision}
}

func matchingQueryScopeContainsMember(memberGroups map[uuid.UUID]map[uuid.UUID]struct{}, memberID uuid.UUID) bool {
	for _, members := range memberGroups {
		if _, ok := members[memberID]; ok {
			return true
		}
	}
	return false
}
