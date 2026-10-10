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

func TestMatchingQueryScopeIsExactAndPartOfPlanDigest(t *testing.T) {
	groupID := uuid.New()
	fileA, fileB := uuid.New(), uuid.New()
	targets := []persistence.MatchingTarget{{GroupID: groupID, Revision: "r1"}}
	groups := map[uuid.UUID]persistence.IncomingGroupingGroup{
		groupID: {ID: groupID, Revision: "r1", Members: []uuid.UUID{fileA, fileB}},
	}
	base := MatchingEvaluationQueryScope{
		GroupIDs:          []uuid.UUID{groupID},
		ReleaseKeysByFile: map[uuid.UUID][]string{fileA: {}, fileB: {"release-b"}},
		SearchKeysByFile: map[uuid.UUID]MatchingSearchKeys{
			fileA: {Release: "release-query-a", Recording: "recording-query-a"},
			fileB: {Release: "release-query-b", Recording: "recording-query-b"},
		},
	}
	canonical, err := canonicalMatchingQueryScope(base, targets, groups)
	if err != nil {
		t.Fatal(err)
	}
	base.ReleaseKeysByFile[fileB][0] = "caller-mutated"
	if canonical.ReleaseKeysByFile[fileB][0] != "release-b" {
		t.Fatal("canonical query scope retained caller-owned release-key slices")
	}

	swapped := MatchingEvaluationQueryScope{
		GroupIDs:          []uuid.UUID{groupID},
		ReleaseKeysByFile: map[uuid.UUID][]string{fileA: {"release-b"}, fileB: {}},
		SearchKeysByFile: map[uuid.UUID]MatchingSearchKeys{
			fileA: {Release: "release-query-b", Recording: "recording-query-b"},
			fileB: {Release: "release-query-a", Recording: "recording-query-a"},
		},
	}
	canonicalSwapped, err := canonicalMatchingQueryScope(swapped, targets, groups)
	if err != nil {
		t.Fatal(err)
	}
	plan := func(scope MatchingEvaluationQueryScope) []byte {
		t.Helper()
		return mustMatchingCoordinatorJSON(t, matchingAdmissionPlan{SchemaVersion: 2, GroupingEpoch: 3, Targets: targets, PolicyVersion: "policy", EvaluationQueryScope: scope})
	}
	if sha256.Sum256(plan(canonical)) == sha256.Sum256(plan(canonicalSwapped)) {
		t.Fatal("swapping per-file query attribution did not change the immutable plan digest")
	}
	var encodedPlan map[string]json.RawMessage
	if err := json.Unmarshal(plan(canonical), &encodedPlan); err != nil {
		t.Fatal(err)
	}
	if _, ok := encodedPlan["evaluation_query_scope"]; !ok {
		t.Fatal("immutable plan omitted the versioned evaluation query scope")
	}
	if _, err := canonicalMatchingQueryScope(MatchingEvaluationQueryScope{
		GroupIDs: []uuid.UUID{groupID}, ReleaseKeysByFile: map[uuid.UUID][]string{fileA: {}, fileB: {}, uuid.New(): {}},
		SearchKeysByFile: map[uuid.UUID]MatchingSearchKeys{fileA: {Release: "a", Recording: "b"}, fileB: {Release: "c", Recording: "d"}},
	}, targets, groups); err == nil {
		t.Fatal("scope with an extra file was accepted")
	}
	if _, err := canonicalMatchingQueryScope(MatchingEvaluationQueryScope{
		GroupIDs: []uuid.UUID{groupID}, ReleaseKeysByFile: map[uuid.UUID][]string{fileA: {}},
		SearchKeysByFile: map[uuid.UUID]MatchingSearchKeys{fileA: {Release: "a", Recording: "b"}},
	}, targets, groups); err == nil {
		t.Fatal("scope missing a selected file was accepted")
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
