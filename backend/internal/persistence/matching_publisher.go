package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
