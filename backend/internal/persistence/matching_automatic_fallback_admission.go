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

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/uptrace/bun"
)

type AutomaticFallbackMatchingAdmission struct {
	Parent                *Operation
	ParentArgs            river.JobArgs
	Provider              MatchingProviderRequest
	Source                AutomaticFallbackProviderSource
	GroupID               uuid.UUID
	ExpectedGroupRevision string
	PolicyVersion         string
	Plan                  policy.TypedFallbackQueryPlan
	EventID               uuid.UUID
	EventRiverJobID       int64
	EventRiverAttempt     int
	EventDeliveryEpoch    int64
	RequestGeneration     int64
}

// AutomaticFallbackProviderSource binds a fallback run to the saved provider
// configuration even when its initial plan has no provider child.
type AutomaticFallbackProviderSource struct {
	ID                    uuid.UUID
	ProviderID            uuid.UUID
	Endpoint              string
	ConfigurationIdentity string
}

// AdmitAutomaticFallbackMatchingRun stores a source-bound finite plan and
// initial child in one transaction. An empty plan creates a waiting_policy
// parent with no provider child; later stage-engine work owns its settlement.
func (repository *SetupManagerRepository) AdmitAutomaticFallbackMatchingRun(ctx context.Context, client RiverInserter, admission AutomaticFallbackMatchingAdmission) (AutomaticArtistMatchingAdmissionResult, error) {
	if client == nil || admission.Parent == nil || admission.Parent.Kind != "matching_evaluation_v1" || admission.GroupID == uuid.Nil || admission.ExpectedGroupRevision == "" || admission.EventID == uuid.Nil || admission.EventRiverJobID < 1 || admission.EventRiverAttempt < 1 || admission.EventDeliveryEpoch < 1 || admission.RequestGeneration < 0 || !matchingpolicy.Supported(admission.PolicyVersion) {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching admission is incomplete")
	}
	planBytes, err := json.Marshal(admission.Plan)
	if err != nil {
		return AutomaticArtistMatchingAdmissionResult{}, err
	}
	planBytes, err = policy.CanonicalFallbackQueryPlan(planBytes)
	if err != nil {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching plan is invalid: %w", err)
	}
	var plan policy.TypedFallbackQueryPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return AutomaticArtistMatchingAdmissionResult{}, err
	}
	provider := admission.Provider.Operation
	if (len(plan.Stages) == 0) != (provider == nil) {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching provider does not match plan")
	}
	if provider != nil {
		if err := validateFallbackProviderInput(plan.Stages[0], provider); err != nil {
			return AutomaticArtistMatchingAdmissionResult{}, err
		}
		if admission.Source.ID == uuid.Nil {
			if provider.ProviderSourceID == nil || provider.ProviderID == nil || provider.ProviderConfigurationIdentity == nil || provider.ProviderEndpoint == "" {
				return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching provider source is incomplete")
			}
			admission.Source = AutomaticFallbackProviderSource{
				ID: *provider.ProviderSourceID, ProviderID: *provider.ProviderID,
				Endpoint: provider.ProviderEndpoint, ConfigurationIdentity: *provider.ProviderConfigurationIdentity,
			}
		}
	}
	if admission.Source.ID == uuid.Nil || admission.Source.ProviderID == uuid.Nil || admission.Source.Endpoint == "" || admission.Source.ConfigurationIdentity == "" {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching source is incomplete")
	}
	if provider != nil && (admission.Source.ID != fallbackProviderUUID(provider) || admission.Source.ProviderID != fallbackProviderID(provider) || admission.Source.Endpoint != fallbackProviderEndpoint(provider) || admission.Source.ConfigurationIdentity != fallbackProviderConfiguration(provider)) {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic fallback matching provider differs from source")
	}
	var result AutomaticArtistMatchingAdmissionResult
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("automatic fallback matching admission: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return err
		}
		if err := validateAutomaticFallbackProviderSourceFence(ctx, tx, admission.Source); err != nil {
			return err
		}
		if provider != nil {
			if err := validateAutomaticArtistProviderFence(ctx, tx, provider); err != nil {
				return err
			}
		}
		var eventValid bool
		if err := tx.NewRaw(`SELECT state='dispatched' AND registered_job_id=? AND highest_delivery_attempt=? AND delivery_claimed AND delivery_epoch=? FROM incoming_matching_reconcile_event WHERE id=? FOR UPDATE`, admission.EventRiverJobID, admission.EventRiverAttempt, admission.EventDeliveryEpoch, admission.EventID).Scan(ctx, &eventValid); err != nil || !eventValid {
			return fmt.Errorf("automatic fallback matching reconciliation delivery fence is stale")
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?),hashtext(?))`, "automatic-artist-admission", admission.GroupID.String()); err != nil {
			return err
		}
		var grouping struct {
			Revision     int64 `bun:"revision"`
			NeedsRefresh bool  `bun:"needs_refresh"`
		}
		if err := tx.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &grouping); err != nil {
			return err
		}
		if grouping.NeedsRefresh {
			return ErrIncomingMatchConflict
		}
		if admission.RequestGeneration > 0 {
			var requested int64
			if err := tx.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, admission.GroupID).Scan(ctx, &requested); err != nil || requested != admission.RequestGeneration {
				return ErrIncomingMatchConflict
			}
		}
		var group struct {
			Revision string `bun:"revision"`
		}
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, admission.GroupID).Scan(ctx, &group); err != nil || group.Revision != admission.ExpectedGroupRevision {
			return ErrIncomingMatchConflict
		}
		authorityRevision, err := requireAutomaticMatchingAuthority(ctx, tx, admission.GroupID)
		if err != nil {
			return err
		}
		capture, err := captureAutomaticArtistTarget(ctx, tx, admission.GroupID, group.Revision, authorityRevision)
		if err != nil {
			return err
		}
		metadata, err := captureMatchingAdmissionMetadata(ctx, tx, []MatchingTarget{{GroupID: admission.GroupID, Revision: group.Revision}})
		if err != nil {
			return err
		}
		tagSets := make([]map[string][]string, 0, len(metadata))
		for _, member := range metadata {
			var tags map[string][]string
			if err := json.Unmarshal(member.Tags, &tags); err != nil {
				return fmt.Errorf("automatic fallback matching captured tags are invalid: %w", err)
			}
			tagSets = append(tagSets, tags)
		}
		expectedPlan, artistBearing := policy.PlanFallbackQueries(tagSets)
		expectedRaw, err := json.Marshal(expectedPlan)
		if err != nil {
			return err
		}
		expectedBytes, err := policy.CanonicalFallbackQueryPlan(expectedRaw)
		if err != nil {
			return fmt.Errorf("automatic fallback matching captured plan is invalid: %w", err)
		}
		if artistBearing || !bytes.Equal(planBytes, expectedBytes) {
			return fmt.Errorf("automatic fallback matching plan differs from captured metadata")
		}
		inputDigests := make(map[uuid.UUID]string)
		for _, member := range capture.Members {
			for _, input := range member.AnalysisInputs {
				if input.Step == "metadata" && input.InputDigest != nil {
					inputDigests[input.WorkID] = *input.InputDigest
				}
			}
		}
		for index := range metadata {
			metadata[index].AnalysisInputSHA256 = inputDigests[metadata[index].WorkID]
		}
		metadataDigest, err := matchingMetadataCaptureDigest(metadata)
		if err != nil {
			return err
		}
		captureBytes, err := json.Marshal(capture)
		if err != nil {
			return err
		}
		captureBytes, targetHash, err := automaticArtistTargetCaptureDigest(captureBytes)
		if err != nil {
			return err
		}
		targetDigest := hex.EncodeToString(targetHash[:])
		semantic, err := json.Marshal(struct {
			Version           int             `json:"version"`
			Target            string          `json:"target_capture_digest"`
			Metadata          string          `json:"metadata_capture_digest"`
			Plan              json.RawMessage `json:"fallback_query_plan"`
			Policy            string          `json:"policy_version"`
			Source            uuid.UUID       `json:"provider_source_id"`
			Provider          uuid.UUID       `json:"provider_id"`
			Endpoint          string          `json:"provider_endpoint"`
			Configuration     string          `json:"provider_configuration_identity"`
			RequestGeneration int64           `json:"request_generation,omitempty"`
		}{3, targetDigest, metadataDigest, planBytes, admission.PolicyVersion, admission.Source.ID, admission.Source.ProviderID, admission.Source.Endpoint, admission.Source.ConfigurationIdentity, admission.RequestGeneration})
		if err != nil {
			return err
		}
		fingerprint := sha256.Sum256(semantic)
		var prior struct {
			RunID       uuid.UUID `bun:"original_run_id"`
			OperationID uuid.UUID `bun:"original_operation_id"`
		}
		err = tx.NewRaw(`SELECT original_run_id,original_operation_id FROM incoming_matching_automatic_admission WHERE fingerprint=?`, fingerprint[:]).Scan(ctx, &prior)
		if err == nil {
			result = AutomaticArtistMatchingAdmissionResult{AlreadyAdmitted: true, OriginalRunID: prior.RunID, OriginalOperationID: prior.OperationID}
			if op, getErr := repository.GetOperationForUpdate(ctx, tx, prior.OperationID); getErr == nil {
				result.Operation = op
			} else if !errors.Is(getErr, sql.ErrNoRows) {
				return getErr
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if admission.Parent.ID == uuid.Nil {
			admission.Parent.ID = uuid.New()
		}
		if provider != nil && provider.ID == uuid.Nil {
			provider.ID = uuid.New()
		}
		planDocument, err := json.Marshal(struct {
			SchemaVersion  int                            `json:"schema_version"`
			Intent         string                         `json:"intent"`
			Mode           string                         `json:"mode"`
			Target         string                         `json:"target_capture_digest"`
			Metadata       string                         `json:"metadata_capture_digest"`
			Policy         string                         `json:"policy_version"`
			Fallback       json.RawMessage                `json:"fallback_query_plan"`
			ProviderSource MatchingFallbackProviderSource `json:"fallback_provider_source"`
		}{5, "automatic_fallback_acquisition", "automatic", targetDigest, metadataDigest, admission.PolicyVersion, planBytes, MatchingFallbackProviderSource{
			ID: admission.Source.ID, ProviderID: admission.Source.ProviderID, Endpoint: admission.Source.Endpoint,
			ConfigurationIdentity: admission.Source.ConfigurationIdentity,
		}})
		if err != nil {
			return err
		}
		planDocument, err = canonicalMatchingJSON(planDocument)
		if err != nil {
			return err
		}
		parentSnapshot, err := json.Marshal(struct {
			SchemaVersion int             `json:"schema_version"`
			Plan          json.RawMessage `json:"plan"`
		}{1, planDocument})
		if err != nil {
			return err
		}
		admission.Parent.InputSnapshot, err = canonicalMatchingJSON(parentSnapshot)
		if err != nil {
			return err
		}
		planHash := sha256.Sum256(planDocument)
		if err := repository.createOperationAndEnqueueTx(ctx, tx, admission.Parent, client, admission.ParentArgs, nil); err != nil {
			return err
		}
		if provider != nil {
			if err := repository.createOperationAndEnqueueTx(ctx, tx, provider, client, admission.Provider.Args, nil); err != nil {
				return err
			}
		}
		sourceID := admission.Source.ID
		configuration := admission.Source.ConfigurationIdentity
		if _, err := tx.NewRaw(`INSERT INTO matching_run(id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,provider_source_id,provider_configuration_identity,policy_version,state,fence_version) VALUES (?,?,?,?,?::jsonb,?,?,?,?,?,'queued',2)`, admission.Parent.ID, admission.Parent.ID, "automatic-fallback:"+hex.EncodeToString(fingerprint[:]), planHash[:], string(planDocument), grouping.Revision, grouping.Revision, sourceID, configuration, admission.PolicyVersion).Exec(ctx); err != nil {
			return err
		}
		if admission.Parent.RiverJobID == nil || *admission.Parent.RiverJobID < 1 {
			return fmt.Errorf("automatic fallback matching parent delivery identity is missing")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,?)`, admission.Parent.ID, *admission.Parent.RiverJobID, admission.Parent.Attempt).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water) VALUES (?,0) ON CONFLICT (group_id) DO NOTHING`, admission.GroupID).Exec(ctx); err != nil {
			return err
		}
		var generation int64
		if err := tx.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1,current_run_id=?,updated_at=now() WHERE group_id=? RETURNING high_water`, admission.Parent.ID, admission.GroupID).Scan(ctx, &generation); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation,authority_revision,target_capture,target_capture_digest,reconciliation_generation) VALUES (?,?,?,?,?,?::jsonb,?,NULLIF(?,0))`, admission.Parent.ID, admission.GroupID, group.Revision, generation, authorityRevision, string(captureBytes), targetHash[:], admission.RequestGeneration).Exec(ctx); err != nil {
			return err
		}
		providerInputs := []MatchingAcquisitionProviderRequest{}
		if provider != nil {
			if provider.ProviderCacheKey == nil {
				return fmt.Errorf("automatic fallback matching provider cache key is missing")
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES (?,?,?)`, admission.Parent.ID, provider.ID, *provider.ProviderCacheKey).Exec(ctx); err != nil {
				return err
			}
			if err := registerFallbackRequest(ctx, tx, admission.Parent.ID, 0, "search", sourceID, configuration, provider); err != nil {
				return err
			}
			providerInputs = append(providerInputs, MatchingAcquisitionProviderRequest{OperationID: provider.ID, Kind: provider.Kind, CacheKey: *provider.ProviderCacheKey, Input: append(json.RawMessage(nil), provider.InputSnapshot...)})
		}
		intent := MatchingAcquisitionIntent{SchemaVersion: 3, FenceVersion: 2, GroupingEpoch: grouping.Revision, PlanDigest: hex.EncodeToString(planHash[:]), CaptureDigest: metadataDigest, CapturedMembers: metadata, PolicyVersion: admission.PolicyVersion, TargetCaptureDigest: targetDigest, FallbackQueryPlan: planBytes,
			FallbackProviderSource: &MatchingFallbackProviderSource{ID: admission.Source.ID, ProviderID: admission.Source.ProviderID, Endpoint: admission.Source.Endpoint, ConfigurationIdentity: admission.Source.ConfigurationIdentity},
			Targets:                []MatchingTarget{{GroupID: admission.GroupID, Revision: group.Revision}}, InitialProviderInputs: providerInputs}
		intentBytes, err := canonicalMatchingAcquisitionIntentBytes(intent)
		if err != nil {
			return err
		}
		intentHash := sha256.Sum256(intentBytes)
		phase := "fallback_search"
		var phaseOutput []byte
		if len(plan.Stages) == 0 {
			phase = "waiting_policy"
			phaseOutput, err = json.Marshal(struct {
				State      string `json:"state"`
				StageIndex *int   `json:"stage_index"`
				Descriptor any    `json:"descriptor"`
				Reason     string `json:"reason"`
			}{State: phase, Reason: "fallback_plan_empty"})
		} else {
			stage := plan.Stages[0]
			stageIndex := 0
			phaseOutput, err = json.Marshal(struct {
				State      string                         `json:"state"`
				StageIndex int                            `json:"stage_index"`
				Descriptor policy.TypedFallbackQueryStage `json:"descriptor"`
			}{State: phase, StageIndex: stageIndex, Descriptor: stage})
		}
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition(run_id,intent_version,intent,intent_digest,current_phase) VALUES (?,3,?::jsonb,?,?)`, admission.Parent.ID, string(intentBytes), intentHash[:], phase).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance) VALUES (?,0,?,'{}'::jsonb,?::jsonb,'[]'::jsonb)`, admission.Parent.ID, phase, string(phaseOutput)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_matching_automatic_admission(fingerprint,original_run_id,original_operation_id) VALUES (?,?,?)`, fingerprint[:], admission.Parent.ID, admission.Parent.ID).Exec(ctx); err != nil {
			return err
		}
		result = AutomaticArtistMatchingAdmissionResult{OriginalRunID: admission.Parent.ID, OriginalOperationID: admission.Parent.ID, Operation: admission.Parent}
		return nil
	})
	return result, err
}

func validateAutomaticFallbackProviderSourceFence(ctx context.Context, tx bun.Tx, source AutomaticFallbackProviderSource) error {
	var config string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &config); err != nil || config != source.ConfigurationIdentity {
		return fmt.Errorf("automatic fallback matching provider configuration changed")
	}
	var current struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, source.ID).Scan(ctx, &current); err != nil || current.ProviderID != source.ProviderID || current.Endpoint != source.Endpoint || current.Identity != config {
		return fmt.Errorf("automatic fallback matching provider source changed")
	}
	return nil
}

func validateFallbackProviderInput(stage policy.TypedFallbackQueryStage, operation *Operation) error {
	kind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		return fmt.Errorf("automatic fallback matching provider stage kind is invalid")
	}
	var input struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	canonical, err := canonicalMatchingJSON(operation.InputSnapshot)
	if err != nil || json.Unmarshal(canonical, &input) != nil || operation.Kind != kind || operation.ProviderCacheKey == nil || input.SchemaVersion != 1 || input.Query != stage.Query || input.Offset != 0 || input.Limit != 10 || *operation.ProviderCacheKey != fmt.Sprintf("%s:0:10:%s", cachePrefix, stage.Query) {
		return fmt.Errorf("automatic fallback matching prepared request differs from its first stage")
	}
	operation.InputSnapshot = canonical
	return nil
}

func fallbackProviderUUID(operation *Operation) uuid.UUID {
	if operation != nil && operation.ProviderSourceID != nil {
		return *operation.ProviderSourceID
	}
	return uuid.Nil
}
func fallbackProviderID(operation *Operation) uuid.UUID {
	if operation != nil && operation.ProviderID != nil {
		return *operation.ProviderID
	}
	return uuid.Nil
}
func fallbackProviderEndpoint(operation *Operation) string {
	if operation != nil {
		return operation.ProviderEndpoint
	}
	return ""
}
func fallbackProviderConfiguration(operation *Operation) string {
	if operation != nil && operation.ProviderConfigurationIdentity != nil {
		return *operation.ProviderConfigurationIdentity
	}
	return ""
}
