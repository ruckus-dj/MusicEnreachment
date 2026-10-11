package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestCanonicalMatchingAcquisitionInputSortsAndFencesSelections(t *testing.T) {
	firstGroup, secondGroup := uuid.New(), uuid.New()
	firstRelease, secondRelease := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	input := MatchingAcquisitionEvaluationInput{
		SchemaVersion: 1,
		GroupIDs:      []uuid.UUID{secondGroup, firstGroup},
		ReleaseIDs:    []string{secondRelease, firstRelease},
	}
	canonical, encoded, digest, err := canonicalMatchingAcquisitionInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.GroupIDs[0].String() > canonical.GroupIDs[1].String() || canonical.ReleaseIDs[0] != firstRelease || len(encoded) == 0 || digest == ([32]byte{}) {
		t.Fatalf("input was not canonicalized: %+v", canonical)
	}
}

func TestCanonicalMatchingAcquisitionInputRejectsInvalidSelections(t *testing.T) {
	groupID := uuid.New()
	validRelease := "00000000-0000-4000-8000-000000000001"
	tests := []struct {
		name  string
		input MatchingAcquisitionEvaluationInput
	}{
		{name: "duplicate group", input: MatchingAcquisitionEvaluationInput{SchemaVersion: 1, GroupIDs: []uuid.UUID{groupID, groupID}, ReleaseIDs: []string{validRelease}}},
		{name: "duplicate release", input: MatchingAcquisitionEvaluationInput{SchemaVersion: 1, GroupIDs: []uuid.UUID{groupID}, ReleaseIDs: []string{validRelease, validRelease}}},
		{name: "noncanonical release", input: MatchingAcquisitionEvaluationInput{SchemaVersion: 1, GroupIDs: []uuid.UUID{groupID}, ReleaseIDs: []string{"not-a-uuid"}}},
		{name: "too many releases", input: MatchingAcquisitionEvaluationInput{SchemaVersion: 1, GroupIDs: []uuid.UUID{groupID}, ReleaseIDs: []string{validRelease, "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000004", "00000000-0000-4000-8000-000000000005", "00000000-0000-4000-8000-000000000006", "00000000-0000-4000-8000-000000000007", "00000000-0000-4000-8000-000000000008", "00000000-0000-4000-8000-000000000009", "00000000-0000-4000-8000-000000000010", "00000000-0000-4000-8000-000000000011"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := canonicalMatchingAcquisitionInput(test.input); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}
}
