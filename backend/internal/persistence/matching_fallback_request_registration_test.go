package persistence

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

func TestValidateFallbackRequestRegistrationBindsRequestAuthority(t *testing.T) {
	requestID, runID, sourceID := uuid.New(), uuid.New(), uuid.New()
	const configurationIdentity = "configuration-identity"
	input := json.RawMessage(`{"schema_version":1,"query":"artist: Example AND recording: Track","offset":0,"limit":10}`)
	canonical, err := canonicalMatchingJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := json.Marshal(policy.TypedFallbackQueryPlan{
		SchemaVersion: 1,
		Stages:        []policy.TypedFallbackQueryStage{{Kind: "track", Query: "artist: Example AND recording: Track", Offset: 0, Limit: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	intent := MatchingAcquisitionIntent{
		SchemaVersion:     matchingAutomaticFallbackAcquisitionIntentVersion,
		FallbackQueryPlan: plan,
		InitialProviderInputs: []MatchingAcquisitionProviderRequest{{
			OperationID: requestID,
			Kind:        "provider_recording_search",
			CacheKey:    "recording-search:0:10:artist: Example AND recording: Track",
			Input:       input,
		}},
	}
	digest := sha256.Sum256(canonical)
	registration := fallbackRequestRegistration{
		RunID:                 runID,
		StageIndex:            0,
		Role:                  "search",
		RequestID:             requestID,
		ProviderSourceID:      sourceID,
		ConfigurationIdentity: configurationIdentity,
		Kind:                  "provider_recording_search",
		CacheKey:              intent.InitialProviderInputs[0].CacheKey,
		CanonicalInput:        canonical,
		InputSHA256:           digest[:],
	}
	validate := func(reg fallbackRequestRegistration, currentIntent MatchingAcquisitionIntent, kind, key string, requestInput json.RawMessage) error {
		return validateFallbackRequestRegistration(reg, "search", currentIntent, runID, requestID, sourceID, configurationIdentity, kind, key, requestInput)
	}
	if err := validate(registration, intent, registration.Kind, registration.CacheKey, input); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*fallbackRequestRegistration, *MatchingAcquisitionIntent)
	}{
		{name: "operation kind", edit: func(_ *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) {}},
		{name: "canonical query input", edit: func(reg *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) {
			reg.CanonicalInput = json.RawMessage(`{"schema_version":1,"query":"forged","offset":0,"limit":10}`)
		}},
		{name: "digest", edit: func(reg *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) {
			reg.InputSHA256 = make([]byte, sha256.Size)
		}},
		{name: "source", edit: func(reg *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) {
			reg.ProviderSourceID = uuid.New()
		}},
		{name: "configuration", edit: func(reg *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) {
			reg.ConfigurationIdentity = "other-configuration"
		}},
		{name: "run", edit: func(reg *fallbackRequestRegistration, _ *MatchingAcquisitionIntent) { reg.RunID = uuid.New() }},
		{name: "descriptor", edit: func(_ *fallbackRequestRegistration, currentIntent *MatchingAcquisitionIntent) {
			currentIntent.FallbackQueryPlan = json.RawMessage(`{"schema_version":1,"stages":[{"kind":"album_track","query":"different","offset":0,"limit":10}]}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reg := registration
			reg.InputSHA256 = append([]byte(nil), registration.InputSHA256...)
			currentIntent := intent
			currentIntent.InitialProviderInputs = append([]MatchingAcquisitionProviderRequest(nil), intent.InitialProviderInputs...)
			test.edit(&reg, &currentIntent)
			kind := registration.Kind
			if test.name == "operation kind" {
				kind = "provider_release_search"
			}
			if err := validate(reg, currentIntent, kind, registration.CacheKey, input); err == nil {
				t.Fatal("invalid request authority accepted")
			}
		})
	}
	if err := validate(registration, intent, registration.Kind, registration.CacheKey, json.RawMessage(`{"schema_version":1,"query":"wrong","offset":0,"limit":10}`)); err == nil {
		t.Fatal("operation input differing from registered canonical input accepted")
	}
}
