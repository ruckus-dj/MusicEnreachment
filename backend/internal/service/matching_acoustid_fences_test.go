package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestCombineMatchingGroupResultsMergesAcoustIDFences(t *testing.T) {
	fileA := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	fileB := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	input := func(file uuid.UUID, key string, expected *string) persistence.IncomingMatchAcoustIDSourceFence {
		return persistence.IncomingMatchAcoustIDSourceFence{FileMemberID: file, CacheKey: key, ExpectedAcceptedRevision: expected}
	}
	rev := "accepted-payload-hash"
	response := persistence.IncomingMatchAcoustIDFence{FileMemberID: fileA, CacheKey: "key-a", AcceptedRevision: rev}
	first := persistence.IncomingMatchEvaluation{Provider: persistence.IncomingMatchProviderFence{
		AcoustID:        []persistence.IncomingMatchAcoustIDFence{response},
		AcoustIDSources: []persistence.IncomingMatchAcoustIDSourceFence{input(fileA, "key-a", &rev)},
	}, Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: uuid.New()}}}
	second := persistence.IncomingMatchEvaluation{Provider: persistence.IncomingMatchProviderFence{
		AcoustID:        []persistence.IncomingMatchAcoustIDFence{response},
		AcoustIDSources: []persistence.IncomingMatchAcoustIDSourceFence{input(fileA, "key-a", &rev), input(fileB, "key-b", nil)},
	}, Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: uuid.New()}}}
	combined, err := combineMatchingGroupResults([]MatchingGroupEvaluationResult{{Result: first}, {Result: second}}, persistence.ProviderMatchingSnapshot{Responses: map[string]*persistence.ProviderCachedResponse{"query": nil}}, nil, []string{"query"})
	if err != nil {
		t.Fatal(err)
	}
	if len(combined.Provider.AcoustID) != 1 || len(combined.Provider.AcoustIDSources) != 2 {
		t.Fatalf("merged fences = %d response, %d source; want 1 and 2", len(combined.Provider.AcoustID), len(combined.Provider.AcoustIDSources))
	}
	if combined.Provider.AcoustIDSources[0].FileMemberID != fileA || combined.Provider.AcoustIDSources[1].FileMemberID != fileB {
		t.Fatalf("source fences are not UUID-sorted: %#v", combined.Provider.AcoustIDSources)
	}
	conflict := second
	conflict.Provider.AcoustIDSources = []persistence.IncomingMatchAcoustIDSourceFence{input(fileA, "different-key", &rev)}
	if _, err := combineMatchingGroupResults([]MatchingGroupEvaluationResult{{Result: first}, {Result: conflict}}, persistence.ProviderMatchingSnapshot{Responses: map[string]*persistence.ProviderCachedResponse{"query": nil}}, nil, []string{"query"}); err == nil {
		t.Fatal("expected conflicting duplicate source fence to be rejected")
	}
}
