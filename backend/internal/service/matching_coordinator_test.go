package service

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestCanonicalMatchingProviderRequests(t *testing.T) {
	base := matchingPlanProviderRequest{
		Kind: "provider_release_search", Snapshot: json.RawMessage(`{"query":"album","schema_version":1}`),
		CacheKey: "release-search:0:25:album", ProviderSourceID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		EndpointConfigurationIdentity: "config-a", ProviderID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	refresh := base
	refresh.ExplicitRefresh = true
	first := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: uuid.MustParse("00000000-0000-0000-0000-000000000010")}}
	second := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: uuid.MustParse("00000000-0000-0000-0000-000000000020")}}

	_, planA := canonicalMatchingProviderRequests([]persistence.MatchingProviderRequest{first, second}, []matchingPlanProviderRequest{refresh, base})
	_, planB := canonicalMatchingProviderRequests([]persistence.MatchingProviderRequest{second, first}, []matchingPlanProviderRequest{base, refresh})
	encodedA, err := json.Marshal(planA)
	if err != nil {
		t.Fatal(err)
	}
	encodedB, err := json.Marshal(planB)
	if err != nil {
		t.Fatal(err)
	}
	if string(encodedA) != string(encodedB) {
		t.Fatalf("canonical plan depends on request order:\n%s\n%s", encodedA, encodedB)
	}
	duplicateA := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: uuid.MustParse("00000000-0000-0000-0000-000000000030")}}
	duplicateB := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: uuid.MustParse("00000000-0000-0000-0000-000000000040")}}
	orderedDuplicates, _ := canonicalMatchingProviderRequests(
		[]persistence.MatchingProviderRequest{duplicateB, duplicateA}, []matchingPlanProviderRequest{base, base},
	)
	if orderedDuplicates[0].Operation.ID != duplicateA.Operation.ID {
		t.Fatal("identical semantic requests did not use operation ID as tie-breaker")
	}

	changedFence := base
	changedFence.EndpointConfigurationIdentity = "config-b"
	encodedChanged, err := json.Marshal(changedFence)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(encodedChanged) == sha256.Sum256(mustMatchingCoordinatorJSON(t, base)) {
		t.Fatal("provider fence changes must change the canonical request digest")
	}
}

func mustMatchingCoordinatorJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
