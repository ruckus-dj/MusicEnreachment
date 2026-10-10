package persistence

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestValidateIncomingMatchEvaluationPreservesUnroundedScoresAndOptionalSHA(t *testing.T) {
	providerID, sourceID, groupID, memberID, workID, releaseID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	evaluation := IncomingMatchEvaluation{
		ExpectedGroupsRevision: "groups-revision",
		ExpectedGroupingEpoch:  8,
		PolicyVersion:          "matching-policy-1",
		Provider: IncomingMatchProviderFence{
			ProviderID: providerID, ProviderSourceID: sourceID,
			Namespace: "fixture", Endpoint: "https://provider.example",
			ConfigurationIdentity: "provider-config", CacheKey: "release-search:fixture",
			AcceptedProjectionRevision: "accepted-projection-revision", ReleaseKeys: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		},
		Groups: []IncomingMatchGroupEvaluation{{
			GroupID: groupID, GroupRevision: "group-revision",
			Members: []IncomingMatchMemberFence{{FileMemberID: memberID, WorkID: workID}},
			Candidates: []IncomingMatchCandidate{{
				FileMemberID: memberID, ProviderID: providerID, ProviderSourceID: sourceID,
				ReleaseID: releaseID, Score: math.Nextafter(0.5, 1), Evidence: json.RawMessage(`{"available":false}`),
			}},
		}},
	}
	if err := validateIncomingMatchEvaluation(evaluation); err != nil {
		t.Fatalf("validate no-SHA evaluation with full precision score: %v", err)
	}
	evaluation.Provider.ReleaseKeys = nil
	if err := validateIncomingMatchEvaluation(evaluation); err != nil {
		t.Fatalf("validate evaluation that inspected no releases: %v", err)
	}
	evaluation.Groups[0].Candidates[0].Score = math.NaN()
	if err := validateIncomingMatchEvaluation(evaluation); err == nil {
		t.Fatal("NaN score unexpectedly passed validation")
	}
}

func TestValidateIncomingMatchEvaluationAllowsPositionReuseAcrossFiles(t *testing.T) {
	providerID, sourceID, groupID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	firstMember, secondMember := uuid.New(), uuid.New()
	evaluation := IncomingMatchEvaluation{
		ExpectedGroupsRevision: "groups-revision", ExpectedGroupingEpoch: 1, PolicyVersion: "policy",
		Provider: IncomingMatchProviderFence{ProviderID: providerID, ProviderSourceID: sourceID, Namespace: "fixture", Endpoint: "https://provider.example", ConfigurationIdentity: "config", CacheKey: "cache", AcceptedProjectionRevision: "accepted-projection-revision", ReleaseKeys: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}},
		Groups: []IncomingMatchGroupEvaluation{{GroupID: groupID, GroupRevision: "group", Members: []IncomingMatchMemberFence{
			{FileMemberID: firstMember, WorkID: uuid.New()}, {FileMemberID: secondMember, WorkID: uuid.New()},
		}, Candidates: []IncomingMatchCandidate{
			{FileMemberID: firstMember, ProviderID: providerID, ProviderSourceID: sourceID, ReleaseID: releaseID, Evidence: json.RawMessage(`{}`), Positions: []IncomingMatchPosition{{TrackID: trackID, Medium: 1, Position: 1}}},
			{FileMemberID: secondMember, ProviderID: providerID, ProviderSourceID: sourceID, ReleaseID: releaseID, Evidence: json.RawMessage(`{}`), Positions: []IncomingMatchPosition{{TrackID: trackID, Medium: 1, Position: 1}}},
		}}},
	}
	if err := validateIncomingMatchEvaluation(evaluation); err != nil {
		t.Fatalf("validate position reuse across distinct files: %v", err)
	}
}

func TestCanonicalReleaseKeysAllowsEmptySetAndRequiresSortedNonEmptyKeys(t *testing.T) {
	if !canonicalReleaseKeys(nil) || !canonicalReleaseKeys([]string{}) {
		t.Fatal("empty evaluated release set should be canonical")
	}
	for _, keys := range [][]string{{"b", "a"}, {"a", "a"}, {"a", ""}} {
		if canonicalReleaseKeys(keys) {
			t.Errorf("canonicalReleaseKeys(%q) = true, want false", keys)
		}
	}
	if !canonicalReleaseKeys([]string{"a", "b"}) {
		t.Fatal("sorted, deduplicated release keys should be canonical")
	}
}
