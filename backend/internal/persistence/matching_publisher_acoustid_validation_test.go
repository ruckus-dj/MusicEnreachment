package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestValidateMatchingPlanQueryScopeAcoustIDFences(t *testing.T) {
	valid := func() (IncomingMatchEvaluation, matchingPublisherQueryScope) {
		member, groupID := uuid.New(), uuid.New()
		scope := matchingPublisherQueryScope{
			ReleaseKeysByFile: map[uuid.UUID][]string{member: {"release-key"}},
			SearchKeysByFile: map[uuid.UUID]struct {
				Release   string `json:"release"`
				Recording string `json:"recording"`
			}{member: {Release: "release-query", Recording: "recording-query"}},
			AcoustIDKeysByFile: map[uuid.UUID]string{member: "acoustid-key"},
		}
		revision := "payload-hash"
		evidence, _ := json.Marshal(map[string]any{"files": []any{map[string]any{
			"file_member_id": member, "release_query_key": "release-query", "recording_query_key": "recording-query",
			"acoustid_cache_evidence": map[string]any{"cache_key": "acoustid-key", "accepted_revision": revision},
		}}})
		queryRevision := "query-revision"
		evaluation := IncomingMatchEvaluation{Provider: IncomingMatchProviderFence{
			CacheRevisions:  map[string]*string{"release-query": &queryRevision, "recording-query": &queryRevision},
			ReleaseKeys:     []string{"release-key"},
			AcoustID:        []IncomingMatchAcoustIDFence{{FileMemberID: member, CacheKey: "acoustid-key", AcceptedRevision: revision}},
			AcoustIDSources: []IncomingMatchAcoustIDSourceFence{{FileMemberID: member, CacheKey: "acoustid-key", ExpectedAcceptedRevision: &revision}},
		}, Groups: []IncomingMatchGroupEvaluation{{GroupID: groupID, Members: []IncomingMatchMemberFence{{FileMemberID: member}}, Evidence: evidence}}}
		return evaluation, scope
	}
	tests := []struct {
		name   string
		mutate func(*IncomingMatchEvaluation, *matchingPublisherQueryScope)
		valid  bool
	}{
		{name: "accepted evidence", valid: true},
		{name: "cache miss explicit null", valid: true, mutate: func(e *IncomingMatchEvaluation, _ *matchingPublisherQueryScope) {
			e.Provider.AcoustID = nil
			e.Provider.AcoustIDSources[0].ExpectedAcceptedRevision = nil
			e.Groups[0].Evidence = replacePublisherAcoustIDEvidence(t, e.Groups[0].Evidence, json.RawMessage("null"))
		}},
		{name: "scoped evidence field missing", mutate: func(e *IncomingMatchEvaluation, _ *matchingPublisherQueryScope) {
			e.Groups[0].Evidence = json.RawMessage(`{"files":[{"file_member_id":"` + e.Groups[0].Members[0].FileMemberID.String() + `","release_query_key":"release-query","recording_query_key":"recording-query"}]}`)
		}},
		{name: "extra source", mutate: func(e *IncomingMatchEvaluation, _ *matchingPublisherQueryScope) {
			e.Provider.AcoustIDSources = append(e.Provider.AcoustIDSources, IncomingMatchAcoustIDSourceFence{FileMemberID: uuid.New(), CacheKey: "extra"})
		}},
		{name: "missing source", mutate: func(e *IncomingMatchEvaluation, _ *matchingPublisherQueryScope) { e.Provider.AcoustIDSources = nil }},
		{name: "cache present revision mismatch", mutate: func(e *IncomingMatchEvaluation, _ *matchingPublisherQueryScope) {
			wrong := "other-payload-hash"
			e.Provider.AcoustIDSources[0].ExpectedAcceptedRevision = &wrong
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation, scope := valid()
			if test.mutate != nil {
				test.mutate(&evaluation, &scope)
			}
			err := validateMatchingPlanQueryScope(scope, evaluation)
			if (err == nil) != test.valid {
				t.Fatalf("validation error = %v, want valid=%v", err, test.valid)
			}
		})
	}
}

func replacePublisherAcoustIDEvidence(t *testing.T, raw json.RawMessage, replacement json.RawMessage) json.RawMessage {
	t.Helper()
	var value struct {
		Files []map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value.Files[0]["acoustid_cache_evidence"] = replacement
	updated, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
