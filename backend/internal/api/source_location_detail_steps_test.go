package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceLocationDetailResponseIncludesPersistedAnalysisSteps(t *testing.T) {
	calculatedAt := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	operationID := uuid.New()
	canonicalID, selectedID := uuid.New(), uuid.New()
	activeVersion := "1.6.0"
	detail := service.SourceLocationDetail{
		RootID: uuid.New(), LocationID: uuid.New(),
		MediaVariantID: &canonicalID, SelectedProbeVariantID: &selectedID,
		ActiveFPCalcVersion: &activeVersion,
		Steps: []service.SourceAnalysisStepDetail{
			{
				Name: "sha256", State: "failed", Attempt: 2,
				SafeError: stringPointer("digest failed"),
				SHA256: &service.SourceSHA256Result{
					Value: "0123abcd", CalculatedAt: &calculatedAt, AppliedOperationID: &operationID,
				},
			},
			{
				Name: "fingerprint", State: "succeeded", Attempt: 1,
				Fingerprint: &service.SourceFingerprintDetail{
					Value: "fingerprint-value", Version: "1.6.1", VersionBanner: "fpcalc version 1.6.1",
					AlgorithmNamespace: "chromaprint", AlgorithmID: 2, Duration: 17.5,
					CalculatedAt: calculatedAt, AppliedOperationID: operationID, ParserContractVersion: 1,
				},
			},
		},
	}

	response, err := sourceLocationDetailResponse(detail)
	if err != nil {
		t.Fatalf("present detail: %v", err)
	}
	if len(response.Steps) != 2 || response.Steps[0].Attempt != 2 || response.Steps[0].SHA256.Value != "0123abcd" {
		t.Fatalf("SHA step = %#v", response.Steps)
	}
	fingerprint := response.Steps[1].Fingerprint
	if fingerprint == nil || fingerprint.Version != "1.6.1" || fingerprint.VersionBanner != "fpcalc version 1.6.1" || fingerprint.ParserContractVersion != 1 || fingerprint.AppliedOperationID != operationID {
		t.Fatalf("fingerprint provenance = %#v", fingerprint)
	}
	if response.ActiveFPCalcVersion == nil || *response.ActiveFPCalcVersion != "1.6.0" || response.SelectedProbeVariantID == nil || *response.SelectedProbeVariantID != selectedID || response.MediaVariantID == nil || *response.MediaVariantID != canonicalID {
		t.Fatalf("active/current and canonical/selected identities = %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode detail: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode detail JSON: %v", err)
	}
	if got := string(body["steps"]); got == "null" || got == "" {
		t.Fatalf("steps JSON = %s, want an array", got)
	}
}

func TestSourceLocationDetailResponseDoesNotInventStepsWithoutWork(t *testing.T) {
	response, err := sourceLocationDetailResponse(service.SourceLocationDetail{RootID: uuid.New(), LocationID: uuid.New()})
	if err != nil {
		t.Fatalf("present detail without work: %v", err)
	}
	if response.Steps == nil || len(response.Steps) != 0 {
		t.Fatalf("steps = %#v, want explicit empty array", response.Steps)
	}
	if response.MatchingEligible {
		t.Fatal("detail without work must not be matching eligible")
	}
}

func stringPointer(value string) *string { return &value }
