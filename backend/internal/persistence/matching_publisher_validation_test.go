package persistence

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

func TestValidateMatchingPlanQueryScope(t *testing.T) {
	evaluation, scope := matchingPublisherValidationFixture(t)
	if err := validateMatchingPlanQueryScope(scope, evaluation); err != nil {
		t.Fatalf("valid version 2 admitted scope rejected: %v", err)
	}

	t.Run("rejects members swapped across groups", func(t *testing.T) {
		evaluation, scope := matchingPublisherValidationFixture(t)
		// Swap group evidence while preserving each group's current member set.
		// The overall selected-member count and global membership remain unchanged.
		evaluation.Groups[0].Evidence, evaluation.Groups[1].Evidence = evaluation.Groups[1].Evidence, evaluation.Groups[0].Evidence
		if err := validateMatchingPlanQueryScope(scope, evaluation); err == nil {
			t.Fatal("expected cross-group member swap to be rejected")
		}
	})

	t.Run("rejects omitted query revision", func(t *testing.T) {
		evaluation, scope := matchingPublisherValidationFixture(t)
		delete(evaluation.Provider.CacheRevisions, "recording-1")
		if err := validateMatchingPlanQueryScope(scope, evaluation); err == nil {
			t.Fatal("expected omitted query revision to be rejected")
		}
	})

	t.Run("rejects substituted query revision key", func(t *testing.T) {
		evaluation, scope := matchingPublisherValidationFixture(t)
		revision := evaluation.Provider.CacheRevisions["recording-1"]
		delete(evaluation.Provider.CacheRevisions, "recording-1")
		evaluation.Provider.CacheRevisions["unadmitted-query"] = revision
		if err := validateMatchingPlanQueryScope(scope, evaluation); err == nil {
			t.Fatal("expected substituted query revision key to be rejected")
		}
	})
}

func matchingPublisherValidationFixture(t *testing.T) (IncomingMatchEvaluation, matchingPublisherQueryScope) {
	t.Helper()
	memberIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	groupIDs := []uuid.UUID{uuid.New(), uuid.New()}
	scope := matchingPublisherQueryScope{
		ReleaseKeysByFile: make(map[uuid.UUID][]string, len(memberIDs)),
		SearchKeysByFile: make(map[uuid.UUID]struct {
			Release   string `json:"release"`
			Recording string `json:"recording"`
		}, len(memberIDs)),
	}
	revisions := make(map[string]*string, len(memberIDs)*2)
	releases := make([]string, 0, len(memberIDs))
	groups := make([]IncomingMatchGroupEvaluation, 0, len(groupIDs))
	for groupIndex, groupID := range groupIDs {
		start := groupIndex * 2
		members := []IncomingMatchMemberFence{{FileMemberID: memberIDs[start]}, {FileMemberID: memberIDs[start+1]}}
		evidenceFiles := make([]struct {
			FileMemberID      uuid.UUID `json:"file_member_id"`
			ReleaseQueryKey   string    `json:"release_query_key"`
			RecordingQueryKey string    `json:"recording_query_key"`
		}, 0, len(members))
		for offset, member := range members {
			ordinal := start + offset
			suffix := strconv.Itoa(ordinal + 1)
			releaseKey := "release-" + suffix
			recordingKey := "recording-" + suffix
			providerReleaseKey := "provider-release-" + suffix
			releases = append(releases, providerReleaseKey)
			scope.ReleaseKeysByFile[member.FileMemberID] = []string{providerReleaseKey}
			scope.SearchKeysByFile[member.FileMemberID] = struct {
				Release   string `json:"release"`
				Recording string `json:"recording"`
			}{Release: releaseKey, Recording: recordingKey}
			revision := "revision"
			revisions[releaseKey] = &revision
			revisions[recordingKey] = &revision
			evidenceFiles = append(evidenceFiles, struct {
				FileMemberID      uuid.UUID `json:"file_member_id"`
				ReleaseQueryKey   string    `json:"release_query_key"`
				RecordingQueryKey string    `json:"recording_query_key"`
			}{FileMemberID: member.FileMemberID, ReleaseQueryKey: releaseKey, RecordingQueryKey: recordingKey})
		}
		evidence, err := json.Marshal(struct {
			Files any `json:"files"`
		}{Files: evidenceFiles})
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, IncomingMatchGroupEvaluation{GroupID: groupID, GroupRevision: "group-revision", Members: members, Evidence: evidence})
	}
	// A requested cache miss still has an explicit key in the fence, with a nil
	// revision representing the missing cache row.
	revisions["recording-4"] = nil
	providerID, sourceID := uuid.New(), uuid.New()
	return IncomingMatchEvaluation{
		Provider: IncomingMatchProviderFence{
			ProviderID: providerID, ProviderSourceID: sourceID, CacheKey: "release-1", CacheRevision: revisions["release-1"],
			CacheRevisions: revisions, ReleaseKeys: releases,
		},
		Groups: groups,
	}, scope
}
