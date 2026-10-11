package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/uptrace/bun"
)

// ManualMatchingRunPlan is the exact captured review context retained for a
// manual release lookup. Plan is caller-prepared and immutable; it is never
// inferred from artist search results or matching scores.
type ManualMatchingRunPlan struct {
	GroupingEpoch int64           `json:"grouping_epoch"`
	PolicyVersion string          `json:"policy_version"`
	Plan          json.RawMessage `json:"plan"`
	GroupContext  json.RawMessage `json:"group_context"`
	SourceContext json.RawMessage `json:"source_context"`
}

// AdmitManualReleaseMatchingRun durably admits exactly one user-selected
// release lookup against the captured evaluation and group. No catalog
// membership or automatic matching policy is implied.
func (repository *SetupManagerRepository) AdmitManualReleaseMatchingRun(
	ctx context.Context,
	client RiverInserter,
	requestID, groupID uuid.UUID,
	expectedGroupRevision string,
	expectedEvaluationRevision *string,
	selectedReleaseMBID uuid.UUID,
	child MatchingProviderRequest,
	plan ManualMatchingRunPlan,
) (*Operation, error) {
	if client == nil || requestID == uuid.Nil || groupID == uuid.Nil || selectedReleaseMBID == uuid.Nil ||
		expectedGroupRevision == "" || expectedEvaluationRevision != nil && *expectedEvaluationRevision == "" || plan.GroupingEpoch < 0 ||
		!matchingpolicy.Supported(plan.PolicyVersion) || !jsonObject(plan.Plan) || !jsonObject(plan.GroupContext) || !jsonObject(plan.SourceContext) {
		return nil, errors.New("manual matching admission is incomplete")
	}
	operation := child.Operation
	if operation == nil || operation.ID == uuid.Nil || operation.Kind != "provider_release_lookup" || operation.State != "queued" ||
		operation.ProviderCacheKey == nil || *operation.ProviderCacheKey != "release-lookup:"+strings.ToLower(selectedReleaseMBID.String()) ||
		child.Args == nil || child.Args.Kind() != "provider_fetch_v1" {
		return nil, errors.New("manual matching release lookup child is invalid")
	}
	var lookup struct {
		SchemaVersion int    `json:"schema_version"`
		MBID          string `json:"mbid"`
	}
	if json.Unmarshal(operation.InputSnapshot, &lookup) != nil || lookup.SchemaVersion != 1 || !strings.EqualFold(lookup.MBID, selectedReleaseMBID.String()) || operation.ProviderExplicitRefresh {
		return nil, errors.New("manual matching release lookup does not match selected release")
	}
	var capturedGroup struct {
		GroupID  uuid.UUID `json:"group_id"`
		Revision string    `json:"revision"`
	}
	if json.Unmarshal(plan.GroupContext, &capturedGroup) != nil || capturedGroup.GroupID != groupID || capturedGroup.Revision != expectedGroupRevision {
		return nil, ErrIncomingMatchConflict
	}
	var args struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
	encodedArgs, err := json.Marshal(child.Args)
	if err != nil || json.Unmarshal(encodedArgs, &args) != nil || args.OperationID != operation.ID {
		return nil, errors.New("manual matching release lookup delivery identity is invalid")
	}
	parentID := uuid.New()
	parent := &Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "manual_lookup", InputSnapshot: append(json.RawMessage(nil), plan.Plan...)}
	fullPlan, err := json.Marshal(struct {
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
	}{1, "manual_review", requestID, groupID, expectedGroupRevision, expectedEvaluationRevision, selectedReleaseMBID, plan.Plan, plan.GroupContext, plan.SourceContext, operation.ID})
	if err != nil {
		return nil, fmt.Errorf("encode manual matching plan: %w", err)
	}
	parent.InputSnapshot = fullPlan
	canonicalInput, err := json.Marshal(struct {
		RequestID     uuid.UUID       `json:"request_id"`
		GroupID       uuid.UUID       `json:"group_id"`
		GroupRevision string          `json:"group_revision"`
		Evaluation    *string         `json:"evaluation_revision"`
		ReleaseMBID   uuid.UUID       `json:"selected_release_mbid"`
		GroupingEpoch int64           `json:"grouping_epoch"`
		PolicyVersion string          `json:"policy_version"`
		Plan          json.RawMessage `json:"plan"`
		GroupContext  json.RawMessage `json:"group_context"`
		SourceContext json.RawMessage `json:"source_context"`
	}{requestID, groupID, expectedGroupRevision, expectedEvaluationRevision, selectedReleaseMBID, plan.GroupingEpoch, plan.PolicyVersion, plan.Plan, plan.GroupContext, plan.SourceContext})
	if err != nil {
		return nil, err
	}
	canonicalDigest := sha256.Sum256(canonicalInput)
	var result *Operation
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("manual matching admission: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("manual matching admission: lock source mutations: %w", err)
		}
		if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))", "manual-matching-admission", requestID.String()); err != nil {
			return err
		}
		var prior struct {
			RunID           uuid.UUID `bun:"run_id"`
			CanonicalDigest []byte    `bun:"canonical_input_digest"`
		}
		err := tx.NewRaw(`SELECT run_id,canonical_input_digest FROM manual_matching_admission WHERE request_id=?`, requestID).Scan(ctx, &prior)
		if err == nil {
			if !equalDigest(prior.CanonicalDigest, canonicalDigest[:]) {
				return ErrIncomingMatchConflict
			}
			if err := validateManualAdmissionCurrentState(ctx, tx, groupID, expectedGroupRevision, expectedEvaluationRevision, plan.GroupingEpoch, prior.RunID); err != nil {
				return err
			}
			result, err = repository.GetOperationForUpdate(ctx, tx, prior.RunID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return err
		}
		var evaluation IncomingMatchEvaluationHeader
		evaluationErr := tx.NewRaw(`SELECT group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,provider_id,provider_source_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,provider_snapshot_revision,provider_fences,evaluated_at FROM incoming_match_evaluation WHERE group_id=? FOR SHARE`, groupID).Scan(ctx, &evaluation)
		if expectedEvaluationRevision == nil {
			if !errors.Is(evaluationErr, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
		} else if evaluationErr != nil || evaluation.EvaluationRevision != *expectedEvaluationRevision || evaluation.GroupRevision != expectedGroupRevision || evaluation.GroupingEpoch != plan.GroupingEpoch || evaluation.PolicyVersion != plan.PolicyVersion {
			return ErrIncomingMatchConflict
		}
		if expectedEvaluationRevision != nil && evaluation.GroupRevision != expectedGroupRevision {
			return ErrIncomingMatchConflict
		}
		var grouping struct {
			Revision     int64 `bun:"revision"`
			NeedsRefresh bool  `bun:"needs_refresh"`
		}
		if err := tx.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &grouping); err != nil || grouping.Revision != plan.GroupingEpoch || grouping.NeedsRefresh {
			return ErrIncomingMatchConflict
		}
		var actualGroupRevision string
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, groupID).Scan(ctx, &actualGroupRevision); err != nil || actualGroupRevision != expectedGroupRevision {
			return ErrIncomingMatchConflict
		}
		var fence IncomingMatchProviderFence
		if expectedEvaluationRevision != nil {
			if err := json.Unmarshal(evaluation.ProviderFences, &fence); err != nil || fence.ProviderID != evaluation.ProviderID || fence.ProviderSourceID != evaluation.ProviderSourceID || fence.ConfigurationIdentity != evaluation.ProviderConfigurationIdentity || fence.Endpoint != evaluation.ProviderEndpoint {
				return ErrIncomingMatchConflict
			}
		} else {
			var source struct {
				ProviderID            uuid.UUID `json:"provider_id"`
				SourceID              uuid.UUID `json:"provider_source_id"`
				Endpoint              string    `json:"endpoint"`
				ConfigurationIdentity string    `json:"configuration_identity"`
			}
			if json.Unmarshal(plan.SourceContext, &source) != nil || source.ProviderID == uuid.Nil || source.SourceID == uuid.Nil || source.Endpoint == "" || source.ConfigurationIdentity == "" {
				return ErrIncomingMatchConflict
			}
			fence.ProviderID, fence.ProviderSourceID = source.ProviderID, source.SourceID
			fence.Endpoint, fence.ConfigurationIdentity = source.Endpoint, source.ConfigurationIdentity
		}
		var currentConfig string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentConfig); err != nil || currentConfig != fence.ConfigurationIdentity {
			return ErrIncomingMatchConflict
		}
		var providerSource struct {
			ProviderID uuid.UUID `bun:"provider_id"`
			Endpoint   string    `bun:"endpoint"`
			Identity   string    `bun:"configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, fence.ProviderSourceID).Scan(ctx, &providerSource); err != nil || providerSource.ProviderID != fence.ProviderID || providerSource.Endpoint != fence.Endpoint || providerSource.Identity != fence.ConfigurationIdentity {
			return ErrIncomingMatchConflict
		}
		var capturedSource struct {
			ProviderID            uuid.UUID `json:"provider_id"`
			SourceID              uuid.UUID `json:"provider_source_id"`
			Endpoint              string    `json:"endpoint"`
			ConfigurationIdentity string    `json:"configuration_identity"`
		}
		if json.Unmarshal(plan.SourceContext, &capturedSource) != nil || capturedSource.ProviderID != fence.ProviderID || capturedSource.SourceID != fence.ProviderSourceID || capturedSource.Endpoint != fence.Endpoint || capturedSource.ConfigurationIdentity != fence.ConfigurationIdentity {
			return ErrIncomingMatchConflict
		}
		if operation.ProviderSourceID == nil || *operation.ProviderSourceID != fence.ProviderSourceID || operation.ProviderID == nil || *operation.ProviderID != fence.ProviderID || operation.ProviderConfigurationIdentity == nil || *operation.ProviderConfigurationIdentity != fence.ConfigurationIdentity || operation.ProviderEndpoint != fence.Endpoint || *operation.ProviderCacheKey != "release-lookup:"+strings.ToLower(selectedReleaseMBID.String()) {
			return errors.New("manual matching lookup provider fence is invalid")
		}
		captured, err := currentManualMatchingMembers(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if expectedEvaluationRevision != nil {
			var evaluated []IncomingMatchMemberFence
			if err := tx.NewRaw(`SELECT file_member_id,work_id,source_sha256 FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &evaluated); err != nil || len(evaluated) == 0 || !sameManualMemberFences(evaluated, captured) {
				return ErrIncomingMatchConflict
			}
		}
		membersJSON, err := json.Marshal(captured)
		if err != nil {
			return err
		}
		planDigestInput, err := json.Marshal(struct {
			CanonicalInput  json.RawMessage            `json:"canonical_input"`
			OriginalMembers []IncomingMatchMemberFence `json:"original_members"`
		}{canonicalInput, captured})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(planDigestInput)
		if err := repository.createOperationAndEnqueueTx(ctx, tx, parent, client, MatchingEvaluationWakeArgs{OperationID: parentID}, nil); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run(id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,provider_source_id,provider_configuration_identity,policy_version,state) VALUES(?,?,?,?,?::jsonb,?,?,?,?,?,'waiting_provider')`, parentID, parentID, "manual:"+requestID.String(), digest[:], string(fullPlan), grouping.Revision, grouping.Revision, fence.ProviderSourceID, fence.ConfigurationIdentity, plan.PolicyVersion).Exec(ctx); err != nil {
			return fmt.Errorf("manual matching admission: create run: %w", err)
		}
		intent, err := json.Marshal(map[string]any{"schema_version": 1, "mode": "manual_review", "request_id": requestID, "group_id": groupID, "group_revision": expectedGroupRevision, "evaluation_revision": expectedEvaluationRevision, "selected_release_mbid": selectedReleaseMBID, "plan_digest": fmt.Sprintf("%x", digest[:]), "lookup_operation_id": operation.ID, "lookup_cache_key": *operation.ProviderCacheKey, "source_id": fence.ProviderSourceID, "provider_id": fence.ProviderID, "configuration_identity": fence.ConfigurationIdentity, "original_members": captured})
		if err != nil {
			return err
		}
		intentDigest := sha256.Sum256(intent)
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition(run_id,intent_version,intent,intent_digest,current_phase,revision) VALUES(?,1,?::jsonb,?,'manual_lookup',0)`, parentID, string(intent), intentDigest[:]).Exec(ctx); err != nil {
			return err
		}
		phaseInput, _ := json.Marshal(map[string]any{"request_id": requestID, "selected_release_mbid": selectedReleaseMBID, "lookup_operation_id": operation.ID, "cache_key": *operation.ProviderCacheKey})
		phaseOutput, _ := json.Marshal(map[string]string{"state": "awaiting_provider"})
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance) VALUES(?,0,'manual_lookup',?::jsonb,?::jsonb,'[]'::jsonb)`, parentID, string(phaseInput), string(phaseOutput)).Exec(ctx); err != nil {
			return err
		}
		if parent.RiverJobID == nil || *parent.RiverJobID < 1 {
			return errors.New("manual matching parent delivery identity is missing")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES(?,?,?)`, parentID, *parent.RiverJobID, parent.Attempt).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water) VALUES(?,0) ON CONFLICT(group_id) DO NOTHING`, groupID).Exec(ctx); err != nil {
			return err
		}
		var generation int64
		if err := tx.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1,current_run_id=?,updated_at=now() WHERE group_id=? RETURNING high_water`, parentID, groupID).Scan(ctx, &generation); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation) VALUES(?,?,?,?)`, parentID, groupID, expectedGroupRevision, generation).Exec(ctx); err != nil {
			return err
		}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, operation, client, child.Args, nil); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES(?,?,?)`, parentID, operation.ID, *operation.ProviderCacheKey).Exec(ctx); err != nil {
			return err
		}
		var evaluationRevision any
		if expectedEvaluationRevision != nil {
			evaluationRevision = *expectedEvaluationRevision
		}
		if _, err := tx.NewRaw(`INSERT INTO manual_matching_admission(request_id,run_id,group_id,group_revision,evaluation_revision,selected_release_mbid,lookup_operation_id,cache_key,canonical_input_digest,plan_digest,original_members) VALUES(?,?,?,?,?,?,?,?,?,?,?::jsonb)`, requestID, parentID, groupID, expectedGroupRevision, evaluationRevision, selectedReleaseMBID, operation.ID, *operation.ProviderCacheKey, canonicalDigest[:], digest[:], string(membersJSON)).Exec(ctx); err != nil {
			return err
		}
		result = parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateManualAdmissionCurrentState(ctx context.Context, tx bun.Tx, groupID uuid.UUID, expectedGroupRevision string, expectedEvaluationRevision *string, groupingEpoch int64, runID uuid.UUID) error {
	var currentGroupRevision string
	if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR SHARE`, groupID).Scan(ctx, &currentGroupRevision); err != nil || currentGroupRevision != expectedGroupRevision {
		return ErrIncomingMatchConflict
	}
	var grouping struct {
		Revision     int64 `bun:"revision"`
		NeedsRefresh bool  `bun:"needs_refresh"`
	}
	if err := tx.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR SHARE`).Scan(ctx, &grouping); err != nil || grouping.Revision != groupingEpoch || grouping.NeedsRefresh {
		return ErrIncomingMatchConflict
	}
	var currentEvaluation string
	evaluationErr := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR SHARE`, groupID).Scan(ctx, &currentEvaluation)
	if expectedEvaluationRevision == nil {
		if !errors.Is(evaluationErr, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
	} else if evaluationErr != nil || currentEvaluation != *expectedEvaluationRevision {
		return ErrIncomingMatchConflict
	}
	current, err := currentManualMatchingMembers(ctx, tx, groupID)
	if err != nil {
		return err
	}
	var originalJSON []byte
	if err := tx.NewRaw(`SELECT original_members FROM manual_matching_admission WHERE run_id=?`, runID).Scan(ctx, &originalJSON); err != nil {
		return ErrIncomingMatchConflict
	}
	var original []IncomingMatchMemberFence
	if json.Unmarshal(originalJSON, &original) != nil || !sameManualMemberFences(original, current) {
		return ErrIncomingMatchConflict
	}
	return nil
}

func currentManualMatchingMembers(ctx context.Context, tx bun.Tx, groupID uuid.UUID) ([]IncomingMatchMemberFence, error) {
	var members []struct {
		FileMemberID uuid.UUID `bun:"variant_id"`
		Ready        bool      `bun:"ready"`
	}
	if err := tx.NewRaw(`SELECT variant_id,ready FROM incoming_group_member WHERE group_id=? ORDER BY variant_id FOR SHARE`, groupID).Scan(ctx, &members); err != nil || len(members) == 0 {
		return nil, ErrIncomingMatchConflict
	}
	captures, err := loadIncomingGroupingCaptures(ctx, tx)
	if err != nil {
		return nil, err
	}
	byMember := make(map[uuid.UUID][]IncomingGroupingCapture, len(members))
	for _, capture := range captures {
		byMember[capture.VariantID] = append(byMember[capture.VariantID], capture)
	}
	result := make([]IncomingMatchMemberFence, 0, len(members))
	for _, member := range members {
		if !member.Ready {
			return nil, ErrIncomingMatchConflict
		}
		var current *IncomingGroupingCapture
		for index := range byMember[member.FileMemberID] {
			capture := &byMember[member.FileMemberID][index]
			if capture.WorkID == uuid.Nil || !capture.Ready || !capture.RootEnabled || capture.RootStale ||
				capture.RootID == uuid.Nil || capture.LocationID == uuid.Nil || capture.ConfiguredPath == "" || capture.InventoryPath != capture.ConfiguredPath || capture.RelativePath == "" ||
				len(capture.SourceSHA256) != 0 && len(capture.SourceSHA256) != sha256.Size {
				continue
			}
			current = capture
			break
		}
		if current == nil {
			return nil, ErrIncomingMatchConflict
		}
		result = append(result, IncomingMatchMemberFence{FileMemberID: member.FileMemberID, WorkID: current.WorkID, SourceSHA256: append([]byte(nil), current.SourceSHA256...)})
	}
	return result, nil
}

func sameManualMemberFences(left, right []IncomingMatchMemberFence) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].FileMemberID != right[index].FileMemberID || left[index].WorkID != right[index].WorkID || string(left[index].SourceSHA256) != string(right[index].SourceSHA256) {
			return false
		}
	}
	return true
}

// ManualMatchingRunSnapshot is the read-only durable state needed by the later
// manual lookup consumer. AcceptedPayloadSHA256 is empty until the dependency
// has settled successfully; the consumer must recheck current fences before use.
type ManualMatchingRunSnapshot struct {
	RequestID                  uuid.UUID       `json:"request_id"`
	RunID                      uuid.UUID       `json:"run_id"`
	GroupID                    uuid.UUID       `json:"group_id"`
	GroupRevision              string          `json:"group_revision"`
	ExpectedEvaluationRevision *string         `json:"expected_evaluation_revision"`
	SelectedReleaseMBID        uuid.UUID       `json:"selected_release_mbid"`
	LookupOperationID          uuid.UUID       `json:"lookup_operation_id"`
	CacheKey                   string          `json:"cache_key"`
	OriginalMembers            json.RawMessage `json:"original_members"`
	AcceptedPayloadSHA256      string          `json:"accepted_payload_sha256,omitempty"`
	AcceptedCacheRevision      *string         `json:"accepted_cache_revision,omitempty"`
	AcceptedProjectionSHA256   string          `json:"accepted_projection_sha256,omitempty"`
	AcceptedProviderSourceID   uuid.UUID       `json:"accepted_provider_source_id,omitempty"`
	AcceptedConfigurationID    string          `json:"accepted_configuration_identity,omitempty"`
	Pending                    bool            `json:"pending"`
}

func (repository *SetupManagerRepository) GetManualReleaseMatchingRun(ctx context.Context, requestID uuid.UUID) (ManualMatchingRunSnapshot, bool, error) {
	var snapshot ManualMatchingRunSnapshot
	var outcome *string
	var cacheRevision *string
	var providerSourceID *uuid.UUID
	var configurationIdentity *string
	var payloadDigest, projectionDigest []byte
	err := repository.db.NewRaw(`SELECT admission.request_id,admission.run_id,admission.group_id,admission.group_revision,admission.evaluation_revision,admission.selected_release_mbid,admission.lookup_operation_id,admission.cache_key,admission.original_members,seal.payload_sha256,seal.cache_revision,seal.projection_sha256,seal.provider_source_id,seal.configuration_identity,dependency.terminal_outcome,(dependency.terminal_outcome IS NULL)
		FROM manual_matching_admission admission JOIN matching_provider_dependency dependency ON dependency.parent_run_id=admission.run_id AND dependency.request_id=admission.lookup_operation_id
		LEFT JOIN manual_matching_lookup_seal seal ON seal.request_id=admission.request_id AND seal.sealed_at IS NOT NULL WHERE admission.request_id=?`, requestID).Scan(ctx, &snapshot.RequestID, &snapshot.RunID, &snapshot.GroupID, &snapshot.GroupRevision, &snapshot.ExpectedEvaluationRevision, &snapshot.SelectedReleaseMBID, &snapshot.LookupOperationID, &snapshot.CacheKey, &snapshot.OriginalMembers, &payloadDigest, &cacheRevision, &projectionDigest, &providerSourceID, &configurationIdentity, &outcome, &snapshot.Pending)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualMatchingRunSnapshot{}, false, nil
	}
	if err != nil {
		return ManualMatchingRunSnapshot{}, false, fmt.Errorf("read manual matching run: %w", err)
	}
	if outcome != nil && *outcome == "succeeded" && len(payloadDigest) == sha256.Size && len(projectionDigest) == sha256.Size {
		snapshot.AcceptedPayloadSHA256 = fmt.Sprintf("%x", payloadDigest)
		snapshot.AcceptedProjectionSHA256 = fmt.Sprintf("%x", projectionDigest)
		snapshot.AcceptedCacheRevision = cacheRevision
		if providerSourceID != nil {
			snapshot.AcceptedProviderSourceID = *providerSourceID
		}
		if configurationIdentity != nil {
			snapshot.AcceptedConfigurationID = *configurationIdentity
		}
	}
	return snapshot, true, nil
}
