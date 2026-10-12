package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/uptrace/bun"
)

type fallbackRequestRegistration struct {
	RunID                 uuid.UUID
	StageIndex            int
	Role                  string
	RequestID             uuid.UUID
	ProviderSourceID      uuid.UUID
	ConfigurationIdentity string
	Kind                  string
	CacheKey              string
	CanonicalInput        json.RawMessage
	InputSHA256           []byte
}

// registerFallbackRequest records admission authority alongside the dependency,
// never by inferring identity from a cache row or an existing receipt.
func registerFallbackRequest(ctx context.Context, tx bun.Tx, runID uuid.UUID, stageIndex int, role string, sourceID uuid.UUID, configurationIdentity string, operation *Operation) error {
	if runID == uuid.Nil || stageIndex < 0 || sourceID == uuid.Nil || configurationIdentity == "" || operation == nil || operation.ID == uuid.Nil ||
		operation.ProviderCacheKey == nil || operation.ProviderSourceID == nil || *operation.ProviderSourceID != sourceID ||
		operation.ProviderConfigurationIdentity == nil || *operation.ProviderConfigurationIdentity != configurationIdentity {
		return fmt.Errorf("register fallback request: request scope is incomplete or unsupported")
	}
	canonical, err := canonicalMatchingJSON(operation.InputSnapshot)
	if err != nil {
		return fmt.Errorf("register fallback request: canonicalize input: %w", err)
	}
	var input struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		MBID          string `json:"mbid"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if err := json.Unmarshal(canonical, &input); err != nil {
		return fmt.Errorf("register fallback request: decode input: %w", err)
	}
	valid := false
	switch role {
	case "search":
		valid = input.SchemaVersion == 1 && input.Query != "" && input.Offset == 0 && input.Limit == 10 &&
			(operation.Kind == "provider_release_search" || operation.Kind == "provider_recording_search")
	case "lookup":
		mbid, parseErr := uuid.Parse(input.MBID)
		valid = input.SchemaVersion == 1 && parseErr == nil && mbid != uuid.Nil && mbid.String() == input.MBID && operation.Kind == "provider_release_lookup" && input.Query == "" && input.Offset == 0 && input.Limit == 0
	}
	if !valid {
		return fmt.Errorf("register fallback request: role, provider kind, and canonical input do not match")
	}
	if role == "search" {
		prefix := "release-search"
		if operation.Kind == "provider_recording_search" {
			prefix = "recording-search"
		}
		if input.Offset != 0 || input.Limit != 10 || *operation.ProviderCacheKey != fmt.Sprintf("%s:%d:%d:%s", prefix, input.Offset, input.Limit, input.Query) {
			return fmt.Errorf("register fallback request: search cache key differs from input")
		}
	} else {
		if *operation.ProviderCacheKey != "release-lookup:"+input.MBID {
			return fmt.Errorf("register fallback request: lookup cache key differs from input")
		}
	}
	digest := sha256.Sum256(canonical)
	_, err = tx.NewRaw(`INSERT INTO matching_fallback_request_registration
		(run_id,stage_index,role,request_id,provider_source_id,configuration_identity,kind,cache_key,canonical_input,input_sha256)
		VALUES (?,?,?,?,?,?,?, ?,?::jsonb,?)`, runID, stageIndex, role, operation.ID, sourceID, configurationIdentity,
		operation.Kind, *operation.ProviderCacheKey, string(canonical), digest[:]).Exec(ctx)
	return err
}

func validateFallbackRequestRegistration(registration fallbackRequestRegistration, expectedRole string, intent MatchingAcquisitionIntent, runID, requestID, sourceID uuid.UUID, configurationIdentity, operationKind, cacheKey string, operationInput json.RawMessage) error {
	if registration.Role != expectedRole || registration.StageIndex < 0 || registration.RunID != runID || registration.RequestID != requestID ||
		registration.ProviderSourceID != sourceID || registration.ConfigurationIdentity != configurationIdentity ||
		registration.Kind != operationKind || registration.CacheKey != cacheKey || len(registration.InputSHA256) != sha256.Size {
		return fmt.Errorf("registered fallback request scope is invalid")
	}
	canonical, err := canonicalMatchingJSON(registration.CanonicalInput)
	registeredIntent := intentRequestInput(intent, expectedRole, requestID)
	if err != nil || !matchingJSONEqual(canonical, operationInput) || expectedRole == "search" && registeredIntent != nil && !matchingJSONEqual(canonical, registeredIntent) ||
		!fallbackStageMatchesRequest(intent, expectedRole, operationKind, canonical, registration.StageIndex) {
		return fmt.Errorf("registered fallback request input differs from immutable request")
	}
	if expectedRole == "search" {
		var plan policy.TypedFallbackQueryPlan
		if json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil || len(plan.Stages) == 0 {
			return fmt.Errorf("registered fallback request descriptor is invalid")
		}
		if registration.StageIndex >= len(plan.Stages) {
			return fmt.Errorf("registered fallback request descriptor is invalid")
		}
		_, cachePrefix, ok := policy.ProviderKindForFallbackStage(plan.Stages[registration.StageIndex].Kind)
		stage := plan.Stages[registration.StageIndex]
		if !ok || cacheKey != fmt.Sprintf("%s:%d:%d:%s", cachePrefix, stage.Offset, stage.Limit, stage.Query) {
			return fmt.Errorf("registered fallback request cache key differs from descriptor")
		}
	}
	digest := sha256.Sum256(canonical)
	if !equalDigest(registration.InputSHA256, digest[:]) {
		return fmt.Errorf("registered fallback request input digest is invalid")
	}
	return nil
}

func intentRequestInput(intent MatchingAcquisitionIntent, role string, requestID uuid.UUID) json.RawMessage {
	if role == "search" {
		if len(intent.InitialProviderInputs) == 1 && intent.InitialProviderInputs[0].OperationID == requestID {
			return intent.InitialProviderInputs[0].Input
		}
	}
	return nil
}

func loadFallbackRequestRegistration(ctx context.Context, database bun.IDB, runID uuid.UUID, stageIndex int, role string, requestID uuid.UUID) (fallbackRequestRegistration, error) {
	var registration fallbackRequestRegistration
	err := database.NewRaw(`SELECT run_id,stage_index,role,request_id,provider_source_id,configuration_identity,kind,cache_key,canonical_input,input_sha256
		FROM matching_fallback_request_registration WHERE run_id=? AND stage_index=? AND role=? AND request_id=?`, runID, stageIndex, role, requestID).Scan(ctx, &registration.RunID, &registration.StageIndex, &registration.Role, &registration.RequestID, &registration.ProviderSourceID, &registration.ConfigurationIdentity, &registration.Kind, &registration.CacheKey, &registration.CanonicalInput, &registration.InputSHA256)
	return registration, err
}

func fallbackStageMatchesRequest(intent MatchingAcquisitionIntent, role, kind string, input json.RawMessage, stageIndexes ...int) bool {
	var plan policy.TypedFallbackQueryPlan
	if json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil || len(plan.Stages) == 0 || intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion {
		return false
	}
	if role == "search" {
		stageIndex := 0
		if len(stageIndexes) > 0 {
			stageIndex = stageIndexes[0]
		}
		if stageIndex < 0 || stageIndex >= len(plan.Stages) {
			return false
		}
		stage := plan.Stages[stageIndex]
		_, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
		return ok && queryStageIdentity(intent, stageIndex, kind,
			fmt.Sprintf("%s:%d:%d:%s", cachePrefix, stage.Offset, stage.Limit, stage.Query), input)
	}
	if role == "lookup" {
		var request struct {
			SchemaVersion int    `json:"schema_version"`
			MBID          string `json:"mbid"`
		}
		var parsed policy.TypedFallbackQueryPlan
		return kind == "provider_release_lookup" && json.Unmarshal(input, &request) == nil && request.SchemaVersion == 1 && request.MBID != "" &&
			json.Unmarshal(intent.FallbackQueryPlan, &parsed) == nil && len(parsed.Stages) > 0
	}
	return false
}

// queryStageIdentity validates a provider request against one exact
// immutable stage. Only stage zero is represented in InitialProviderInputs;
// later stages are authorized by their durable registration instead.
func queryStageIdentity(intent MatchingAcquisitionIntent, stageIndex int, kind, cacheKey string, input json.RawMessage) bool {
	var plan policy.TypedFallbackQueryPlan
	if intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion || json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil ||
		stageIndex < 0 || stageIndex >= len(plan.Stages) {
		return false
	}
	stage := plan.Stages[stageIndex]
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	var request struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if !ok || providerKind != kind || json.Unmarshal(input, &request) != nil || request.SchemaVersion != 1 ||
		request.Query != stage.Query || request.Offset != stage.Offset || request.Limit != stage.Limit ||
		cacheKey != fmt.Sprintf("%s:%d:%d:%s", cachePrefix, stage.Offset, stage.Limit, stage.Query) {
		return false
	}
	if stageIndex == 0 {
		return len(intent.InitialProviderInputs) == 1 && intent.InitialProviderInputs[0].Kind == kind &&
			matchingJSONEqual(intent.InitialProviderInputs[0].Input, input) && intent.InitialProviderInputs[0].CacheKey == cacheKey
	}
	return true
}
