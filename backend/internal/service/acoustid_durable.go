package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/acoustid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// acoustIDLookupInput is copied from saved selected provenance during admission.
// Workers use this immutable copy and never reread a canonical row.
type acoustIDLookupInput struct {
	WorkID                  uuid.UUID `json:"work_id"`
	FingerprintRowID        uuid.UUID `json:"fingerprint_row_id"`
	WinningResultID         uuid.UUID `json:"winning_result_id"`
	WinnerFingerprint       string    `json:"winner_fingerprint"`
	AlgorithmNamespace      string    `json:"algorithm_namespace"`
	AlgorithmID             int       `json:"algorithm_id"`
	FPCalcVersion           string    `json:"fpcalc_version"`
	ParserContractVersion   int       `json:"parser_contract_version"`
	SavedReportedDuration   float64   `json:"saved_reported_duration"`
	SHA256                  string    `json:"sha256,omitempty"`
	ImmutableMemberIdentity string    `json:"immutable_member_identity,omitempty"`
}

type acoustIDLookupIntent struct {
	SchemaVersion int `json:"schema_version"`
	acoustIDLookupInput
	LookupDuration int `json:"lookup_duration"`
}

type acoustIDCachedResponse struct {
	Available bool             `json:"available"`
	Results   []acoustid.Match `json:"results"`
	Raw       json.RawMessage  `json:"raw"`
}

// CachedAcoustIDEvidence reads only the accepted response for this captured
// file cache key. It does not query the provider or join entity-global data.
func (service *ProviderFetchService) CachedAcoustIDEvidence(ctx context.Context, sourceID uuid.UUID, cacheKey, recordingID string) (*float64, bool, error) {
	response, err := service.cache.GetCachedResponse(ctx, sourceID, cacheKey)
	if err != nil || response == nil || len(response.Payload) == 0 {
		return nil, false, err
	}
	var cached acoustIDCachedResponse
	if err := json.Unmarshal(response.Payload, &cached); err != nil {
		return nil, false, fmt.Errorf("decode cached AcoustID response: %w", err)
	}
	if !cached.Available {
		return nil, false, nil
	}
	return acoustid.MaxScoreForRecording(acoustid.LookupResult{Available: true, Matches: cached.Results}, recordingID), true, nil
}

// StartAcoustIDLookup admits a durable, ID-only provider delivery. Cache
// identity is a versioned digest of captured input, never the application key.
func (service *ProviderFetchService) StartAcoustIDLookup(ctx context.Context, workID uuid.UUID, explicitRefresh bool) (*persistence.Operation, error) {
	if service.operations == nil || service.river == nil {
		return nil, fmt.Errorf("durable provider operations are unavailable")
	}
	var operation *persistence.Operation
	operationID := uuid.New()
	args := ProviderFetchOperationArgs{OperationID: operationID}
	if err := service.operations.CreateAcoustIDLookupOperationAndEnqueue(ctx, workID, explicitRefresh, service.river, args, nil, func(capture persistence.AcoustIDLookupCapture) (*persistence.Operation, error) {
		input := acoustIDLookupInput{
			WorkID: capture.WorkID, FingerprintRowID: capture.FingerprintRowID, WinningResultID: capture.WinningResultID,
			WinnerFingerprint: capture.WinnerFingerprint, AlgorithmNamespace: capture.AlgorithmNamespace,
			AlgorithmID: capture.AlgorithmID, FPCalcVersion: capture.FPCalcVersion, ParserContractVersion: capture.ParserContractVersion,
			SavedReportedDuration: capture.SavedReportedDuration, SHA256: capture.SHA256, ImmutableMemberIdentity: capture.ImmutableMemberIdentity,
		}
		lookupDuration, valid := acoustid.LookupDuration(input.SavedReportedDuration)
		if !valid {
			return nil, fmt.Errorf("AcoustID lookup input is invalid")
		}
		intent := acoustIDLookupIntent{SchemaVersion: 1, acoustIDLookupInput: input, LookupDuration: lookupDuration}
		snapshot, err := json.Marshal(intent)
		if err != nil {
			return nil, fmt.Errorf("encode AcoustID lookup intent: %w", err)
		}
		canonical, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode AcoustID cache identity: %w", err)
		}
		digest := sha256.Sum256(canonical)
		cacheKey := "acoustid-lookup-v1:" + hex.EncodeToString(digest[:])
		sourceID, identity := capture.ProviderSourceID, capture.ConfigurationIdentity
		operation = &persistence.Operation{
			ID: operationID, Kind: "provider_acoustid_lookup", State: "queued", Stage: "queued", InputSnapshot: snapshot,
			ProviderSourceID: &sourceID, ProviderConfigurationIdentity: &identity,
			ProviderCacheKey: &cacheKey, ProviderExplicitRefresh: explicitRefresh,
		}
		return operation, nil
	}); err != nil {
		return nil, err
	}
	return operation, nil
}

func (service *ProviderFetchService) executeAcoustIDOperation(ctx context.Context, operations *Operations, operation *persistence.Operation, epoch, jobID int64) error {
	fail := func() error {
		return operations.FailForProviderDelivery(ctx, operation, epoch, "provider_fetch_failed", "AcoustID lookup could not be completed; cached data was retained.")
	}
	if operation.ProviderSourceID == nil || operation.ProviderConfigurationIdentity == nil || operation.ProviderCacheKey == nil {
		return fail()
	}
	var intent acoustIDLookupIntent
	decoder := json.NewDecoder(strings.NewReader(string(operation.InputSnapshot)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil || intent.SchemaVersion != 1 {
		return fail()
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fail()
	}
	if err := operations.RunningForProviderDelivery(ctx, operation, epoch, "fetching_acoustid"); err != nil {
		return err
	}
	cached, err := service.cache.GetCurrentAcoustIDResponse(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey)
	if err != nil {
		return fail()
	}
	if cached != nil && len(cached.Payload) > 0 && !operation.ProviderExplicitRefresh {
		return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_cache_reused")
	}
	_, present, currentRevision, err := service.registry.ReadAcoustIDCredential(ctx)
	if err != nil || !present || currentRevision != *operation.ProviderConfigurationIdentity {
		return fail()
	}
	duration, validDuration := acoustid.LookupDuration(intent.SavedReportedDuration)
	if !validDuration || duration != intent.LookupDuration {
		return fail()
	}
	ticket, err := service.cache.BeginAcoustIDRefresh(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey, *operation.ProviderConfigurationIdentity)
	if err != nil {
		return fail()
	}
	result, err := service.factory.LookupAcoustIDUnderRevision(ctx, *operation.ProviderConfigurationIdentity, intent.WinnerFingerprint, intent.LookupDuration)
	if err != nil || !result.Available {
		return fail()
	}
	payload, err := json.Marshal(acoustIDCachedResponse{Available: result.Available, Results: result.Matches, Raw: result.Raw})
	if err != nil {
		return fail()
	}
	if err := service.cache.ApplyAcoustIDResponse(ctx, ticket, *operation.ProviderConfigurationIdentity, service.now().UTC(), payload, persistence.ProviderDeliveryFence{
		OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch,
	}); err != nil {
		return fail()
	}
	return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_response_saved")
}
