package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeMatchingDecisionRejectsUnsupportedOrInvalidRecords(t *testing.T) {
	groupID, releaseID, fileID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	valid := MatchingEvaluationDecision{
		Version: matchingDecisionVersion, GroupID: groupID, PolicyVersion: MatchingPolicyVersion,
		Outcome:         MatchingDecisionOutcomeUnique,
		SelectedRelease: &MatchingDecisionSelectedRelease{ProviderReleaseID: releaseID, ProviderReleaseKey: "release-key"},
		Assignments:     []MatchingDecisionAssignment{{FileMemberID: fileID, ProviderTrackID: trackID, Medium: 1, Position: 1}},
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(payload string) error {
		returnErr := json.RawMessage(`{"evaluation_decision":` + payload + `}`)
		_, err := DecodeMatchingDecision(returnErr)
		return err
	}
	if _, err := DecodeMatchingDecision(json.RawMessage(`{"evaluation_decision":` + string(encoded) + `}`)); err != nil {
		t.Fatalf("decode valid decision: %v", err)
	}
	if err := decode(strings.Replace(string(encoded), `"version":1`, `"version":2`, 1)); err == nil {
		t.Fatal("unsupported version was accepted")
	}
	unsupportedPolicy := valid
	unsupportedPolicy.PolicyVersion = "matching-go-v1"
	unsupportedPolicyJSON, _ := json.Marshal(unsupportedPolicy)
	if err := decode(string(unsupportedPolicyJSON)); err == nil {
		t.Fatal("unsupported policy version was accepted")
	}
	if err := decode(strings.TrimSuffix(string(encoded), "}") + `,"unexpected":true}`); err == nil {
		t.Fatal("unknown field was accepted")
	}
	duplicate := valid
	duplicate.Assignments = append(append([]MatchingDecisionAssignment(nil), valid.Assignments...), valid.Assignments[0])
	duplicateJSON, _ := json.Marshal(duplicate)
	if err := decode(string(duplicateJSON)); err == nil {
		t.Fatal("duplicate file member was accepted")
	}
}
