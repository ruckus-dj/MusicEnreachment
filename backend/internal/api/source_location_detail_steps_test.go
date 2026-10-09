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

func TestSourceLocationDetailResponseProjectsStagedArtifactAndExplicitAbsence(t *testing.T) {
	artifactID, creatorID, borrowerID := uuid.New(), uuid.New(), uuid.New()
	safeError := "cleanup could not be completed"
	response, err := sourceLocationDetailResponse(service.SourceLocationDetail{
		RootID: uuid.New(), LocationID: uuid.New(),
		StagedArtifact: service.SourceStagedArtifactDetail{
			ID: &artifactID, State: "cleanup_failed", RequestedSteps: []string{"sha256", "fingerprint"}, RequestedStepsKnown: true,
			CreatorOperationID: &creatorID, BorrowerOperationID: &borrowerID,
			SafeError:          &safeError,
			RelativeOutputPath: stringPointer("analysis/staging/root/work/artifact"),
		},
	})
	if err != nil {
		t.Fatalf("present staged artifact: %v", err)
	}
	artifact := response.StagedArtifact
	if artifact.ArtifactID == nil || *artifact.ArtifactID != artifactID || artifact.State != "cleanup_failed" || len(artifact.RequestedSteps) != 2 || !artifact.RequestedStepsKnown || artifact.CreatorOperationID == nil || *artifact.CreatorOperationID != creatorID || artifact.BorrowerOperationID == nil || *artifact.BorrowerOperationID != borrowerID || artifact.SafeError == nil || *artifact.SafeError != safeError {
		t.Fatalf("staged artifact response = %#v", artifact)
	}
	if artifact.RelativeOutputPath == nil || *artifact.RelativeOutputPath != "analysis/staging/root/work/artifact" {
		t.Fatalf("relative output path = %#v", artifact.RelativeOutputPath)
	}

	withoutArtifact, err := sourceLocationDetailResponse(service.SourceLocationDetail{
		RootID: uuid.New(), LocationID: uuid.New(),
		StagedArtifact: service.SourceStagedArtifactDetail{State: "unknown", RequestedSteps: []string{}},
	})
	if err != nil {
		t.Fatalf("present absent artifact: %v", err)
	}
	absent := withoutArtifact.StagedArtifact
	if absent.State != "unknown" || absent.ArtifactID != nil || absent.CreatorOperationID != nil || absent.BorrowerOperationID != nil || absent.SafeError != nil || absent.RelativeOutputPath != nil || len(absent.RequestedSteps) != 0 || absent.RequestedStepsKnown {
		t.Fatalf("absent artifact was invented: %#v", absent)
	}
	encoded, err := json.Marshal(withoutArtifact)
	if err != nil {
		t.Fatalf("encode absent artifact: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode absent artifact JSON: %v", err)
	}
	if string(body["staged_artifact"]) == "null" || string(body["staged_artifact"]) == "" {
		t.Fatalf("staged_artifact = %s, want the explicit unknown object", body["staged_artifact"])
	}
	var stagedArtifact struct {
		State               string `json:"state"`
		RequestedStepsKnown bool   `json:"requested_steps_known"`
	}
	if err := json.Unmarshal(body["staged_artifact"], &stagedArtifact); err != nil {
		t.Fatalf("decode staged artifact JSON: %v", err)
	}
	if stagedArtifact.State != "unknown" || stagedArtifact.RequestedStepsKnown {
		t.Fatalf("staged_artifact = %#v, want unknown with unknown requested steps", stagedArtifact)
	}
}

func stringPointer(value string) *string { return &value }
