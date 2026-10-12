package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestFallbackCalculatorAcceptsFullPersistedCaptureAndDeduplicatesSharedWork(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 1, false)
	var target map[string]any
	if err := json.Unmarshal(input.Target.Capture, &target); err != nil {
		t.Fatal(err)
	}
	target["authority_revision"] = float64(7)
	members := target["members"].([]any)
	member := members[0].(map[string]any)
	locations := []any{
		persistedLocation(uuid.New(), input.Intent.CapturedMembers[0].MemberID, input.Intent.CapturedMembers[0].WorkID),
		persistedLocation(uuid.New(), input.Intent.CapturedMembers[0].MemberID, input.Intent.CapturedMembers[0].WorkID),
	}
	member["locations"] = locations
	captures := member["captures"].([]any)
	secondCapture := map[string]any{}
	for key, value := range captures[0].(map[string]any) {
		secondCapture[key] = value
	}
	secondCapture["LocationID"] = uuid.New().String()
	secondCapture["RelativePath"] = "second/song.flac"
	member["captures"] = append(captures, secondCapture)
	analysisInputs := member["analysis_inputs"].([]any)
	for _, raw := range analysisInputs {
		raw.(map[string]any)["state"] = "succeeded"
	}
	for _, step := range []string{"fingerprint", "probe", "sha256"} {
		extra := make(map[string]any)
		for key, value := range analysisInputs[0].(map[string]any) {
			extra[key] = value
		}
		extra["analysis_record_id"] = input.Intent.CapturedMembers[0].WorkID.String() + ":" + step
		extra["step"] = step
		extra["input_snapshot"] = json.RawMessage(`null`)
		extra["input_digest"] = nil
		extra["result_reference"] = nil
		member["analysis_inputs"] = append(member["analysis_inputs"].([]any), extra)
	}
	// Persistence repeats the complete work-analysis inventory for each location.
	for _, raw := range append([]any(nil), member["analysis_inputs"].([]any)...) {
		duplicate := make(map[string]any)
		for key, value := range raw.(map[string]any) {
			duplicate[key] = value
		}
		member["analysis_inputs"] = append(member["analysis_inputs"].([]any), duplicate)
	}
	input.Target.Capture = marshalFallbackTarget(t, target)
	refreshFallbackTargetDigest(&input)

	result, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatalf("full persisted target capture was rejected: %v", err)
	}
	if result.QualifyingEdges != 1 {
		t.Fatalf("duplicate locations multiplied durable work edges: got %d, want 1", result.QualifyingEdges)
	}
}

func TestFallbackCalculatorRejectsConflictingDuplicateAnalysisRecord(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 1, false)
	var target map[string]any
	if err := json.Unmarshal(input.Target.Capture, &target); err != nil {
		t.Fatal(err)
	}
	member := target["members"].([]any)[0].(map[string]any)
	analysis := member["analysis_inputs"].([]any)[0].(map[string]any)
	duplicate := make(map[string]any)
	for key, value := range analysis {
		duplicate[key] = value
	}
	duplicate["state"] = "failed"
	member["analysis_inputs"] = append(member["analysis_inputs"].([]any), duplicate)
	input.Target.Capture = marshalFallbackTarget(t, target)
	refreshFallbackTargetDigest(&input)
	if _, err := CalculateFallbackApplicability(input); err == nil {
		t.Fatal("conflicting duplicate analysis records were accepted")
	}
}

func TestFallbackApplicabilityDigestCanonicalizesAuthorityLookupsWithoutMutation(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 2, false)
	original := append([]MatchingFallbackLookupAuthority(nil), input.Authority.Lookups...)
	first, err := fallbackApplicabilityInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Authority.Lookups) != len(original) {
		t.Fatal("digest changed authority lookup slice length")
	}
	if !reflect.DeepEqual(input.Authority.Lookups, original) {
		t.Fatal("digest mutated caller-owned authority lookups")
	}
	for left, right := 0, len(input.Authority.Lookups)-1; left < right; left, right = left+1, right-1 {
		input.Authority.Lookups[left], input.Authority.Lookups[right] = input.Authority.Lookups[right], input.Authority.Lookups[left]
	}
	second, err := fallbackApplicabilityInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("authority lookup order changed digest: %s != %s", first, second)
	}
}

func persistedLocation(locationID, memberID, workID uuid.UUID) map[string]any {
	return map[string]any{
		"VariantID": memberID.String(), "RootID": uuid.New().String(), "WorkID": workID.String(),
		"LocationID": locationID.String(), "ConfiguredPath": "/music", "InventoryPath": "/music",
		"RelativePath": locationID.String() + ".flac", "SizeBytes": float64(100), "Mtime": "2025-01-01T00:00:00Z",
	}
}

func marshalFallbackTarget(t *testing.T, target map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalFallbackJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
