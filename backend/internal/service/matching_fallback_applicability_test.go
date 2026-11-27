package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestFrozenFallbackAuthorityViewPopulatesNoQueryRunAuthority(t *testing.T) {
	seal := persistence.MatchingFallbackSealAuthority{
		SourceID: uuid.New(), ProviderID: uuid.New(), Endpoint: "https://provider.example",
		RunProviderConfiguration: "{}",
	}

	authority := frozenFallbackAuthorityView(seal)
	if authority.RunSourceID != seal.SourceID || authority.RunProviderID != seal.ProviderID ||
		authority.RunEndpoint != seal.Endpoint || authority.RunConfiguration != seal.RunProviderConfiguration {
		t.Fatalf("no-query run authority = %#v, want values copied from seal", authority)
	}
	if authority.CalculationStageIndex != 0 || len(authority.FullEvaluationContext) != 0 {
		t.Fatalf("no-query authority contains fabricated calculation data: %#v", authority)
	}
}

func TestAcceptedFallbackSearchScoresScopesRecordingScoresAndPreservesMissing(t *testing.T) {
	payload := json.RawMessage(`{"recordings":[{"id":"recording-a","score":0,"releases":[{"id":"release-a"},{"id":"release-other"}]},{"id":"recording-b","releases":[{"id":"release-a"}]}]}`)
	scores, err := acceptedFallbackSearchScores("recording-search:key", "provider_recording_search", payload, []string{"release-a"})
	if err != nil {
		t.Fatal(err)
	}
	byRecording := scores["recording-search:key"]
	if got := byRecording["recording-a"]; !got.Present || got.Value != 0 {
		t.Fatalf("explicit zero score = %#v, want present zero", got)
	}
	if got := byRecording["recording-b"]; got.Present || got.Value != 0 {
		t.Fatalf("missing score = %#v, want absent zero", got)
	}
	if _, invented := byRecording["release-other"]; invented {
		t.Fatal("nested release was incorrectly treated as a top-level scored candidate")
	}
	// Provider-accepted MBIDs may arrive uppercase while the hydrated
	// projection uses canonical lowercase identity.
	uuidPayload := json.RawMessage(`{"recordings":[{"id":"1A2B3C4D-5E6F-4A7B-8C9D-0E1F2A3B4C5D","score":42,"releases":[{"id":"0A1B2C3D-4E5F-4A6B-8C7D-9E0F1A2B3C4D"}]}]}`)
	uuidScores, err := acceptedFallbackSearchScores("recording-search:key", "provider_recording_search", uuidPayload, []string{"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"})
	if err != nil {
		t.Fatal(err)
	}
	if got := uuidScores["recording-search:key"]["1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"]; !got.Present || got.Value != 42 {
		t.Fatalf("uppercase accepted MBID score = %#v, want present 42 under canonical identity", got)
	}
	leaked, err := acceptedFallbackSearchScores("recording-search:key", "provider_recording_search", uuidPayload, []string{"2b3c4d5e-6f7a-4b8c-8d9e-0f1a2b3c4d5e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := leaked["recording-search:key"]["1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"]; present {
		t.Fatalf("uppercase accepted MBID score leaked to a second edition: %#v", leaked["recording-search:key"])
	}
	releaseScores, err := acceptedFallbackSearchScores("release-search:key", "provider_release_search", json.RawMessage(`{"releases":[{"id":"0A1B2C3D-4E5F-4A6B-8C7D-9E0F1A2B3C4D","score":77}]}`), []string{"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"})
	if err != nil {
		t.Fatal(err)
	}
	if got := releaseScores["release-search:key"]["0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"]; !got.Present || got.Value != 77 {
		t.Fatalf("uppercase accepted release MBID score = %#v, want present 77 under canonical identity", got)
	}
}
