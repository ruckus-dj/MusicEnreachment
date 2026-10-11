package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PrepareSealed evaluates the exact release graph and accepted response keys
// saved by the acquisition seal. It deliberately supplies no search keys, so
// the existing scoring kernel treats those factors as missing.
func (evaluator *MatchingEvaluationService) PrepareSealed(ctx context.Context, input persistence.MatchingAcquisitionEvaluationInput) (PreparedMatchingEvaluation, error) {
	return evaluator.prepareSealed(ctx, input, nil)
}

type matchingRunFenceReader interface {
	GetMatchingRunFence(context.Context, uuid.UUID) (persistence.MatchingRunFenceSnapshot, bool, error)
}

// PrepareSealedForRun binds acquisition results to the immutable target capture
// registered at admission. It does not widen the sealed cache-key scope.
func (evaluator *MatchingEvaluationService) PrepareSealedForRun(ctx context.Context, reader matchingRunFenceReader, runID uuid.UUID, input persistence.MatchingAcquisitionEvaluationInput) (PreparedMatchingEvaluation, error) {
	if reader == nil {
		return PreparedMatchingEvaluation{}, fmt.Errorf("matching run fence reader is unavailable")
	}
	fence, exists, err := reader.GetMatchingRunFence(ctx, runID)
	if err != nil {
		return PreparedMatchingEvaluation{}, err
	}
	if !exists || fence.Version != 2 || len(input.GroupIDs) != 1 || input.GroupIDs[0] != fence.GroupID {
		return PreparedMatchingEvaluation{}, fmt.Errorf("sealed matching scope differs from immutable target")
	}
	prepared, err := evaluator.prepareSealed(ctx, input, &fence)
	if err != nil {
		return prepared, err
	}
	prepared.evaluation.TargetFenceVersion = 2
	prepared.evaluation.TargetCaptureDigest = fence.CaptureDigest
	return prepared, nil
}

func (evaluator *MatchingEvaluationService) prepareSealed(ctx context.Context, input persistence.MatchingAcquisitionEvaluationInput, fence *persistence.MatchingRunFenceSnapshot) (PreparedMatchingEvaluation, error) {
	var prepared PreparedMatchingEvaluation
	if evaluator == nil || evaluator.groups == nil || evaluator.captures == nil || evaluator.provider == nil || evaluator.results == nil || evaluator.config == nil {
		return prepared, fmt.Errorf("matching evaluation dependencies are unavailable")
	}
	if (input.SchemaVersion != 1 && input.SchemaVersion != 2) || len(input.GroupIDs) == 0 || len(input.ReleaseIDs) > 10 ||
		input.SchemaVersion == 2 && (len(input.ReleaseLookupCacheKeys) != len(input.ReleaseIDs) || len(input.CatalogCacheKeys) == 0) {
		return prepared, fmt.Errorf("sealed matching evaluation scope is invalid")
	}
	expected, err := captureMatchingEvaluationRevisions(ctx, input.GroupIDs, evaluator.results)
	if err != nil {
		return prepared, err
	}
	prepared.ExpectedRevisions = expected
	grouping, err := evaluator.groups.Snapshot(ctx)
	if err != nil {
		return prepared, err
	}
	groups, err := requestedMatchingGroups(grouping.Groups, input.GroupIDs)
	if err != nil {
		return prepared, err
	}
	if fence != nil && (len(groups) != 1 || groups[0].Revision != fence.GroupRevision) {
		return prepared, ErrMatchingEvaluationDeferred
	}
	for _, group := range groups {
		if len(group.UnreadyMembers) != 0 {
			return prepared, ErrMatchingEvaluationDeferred
		}
	}
	captures, err := evaluator.captures.CurrentCaptures(ctx)
	if err != nil {
		return prepared, err
	}
	config, err := evaluator.config.GetMusicBrainzConfig(ctx)
	if err != nil {
		return prepared, err
	}
	if config.Identity == "" {
		return prepared, fmt.Errorf("matching evaluation requires saved MusicBrainz configuration identity")
	}
	memberIDs := make(map[uuid.UUID]bool)
	for _, group := range groups {
		for _, member := range group.Members {
			memberIDs[member] = true
		}
	}
	if input.SchemaVersion == 1 && len(input.EvidenceCacheKeysByFile) != len(memberIDs) ||
		input.SchemaVersion == 2 && len(input.EvidenceCacheKeysByFile) != 0 {
		return prepared, fmt.Errorf("sealed matching evaluation response provenance does not cover its members")
	}
	releaseSet, evidenceSet := make(map[string]bool), make(map[string]bool)
	for _, releaseID := range input.ReleaseIDs {
		id, parseErr := uuid.Parse(releaseID)
		if parseErr != nil || id.String() != releaseID {
			return prepared, fmt.Errorf("sealed matching evaluation contains an invalid release identity")
		}
		releaseSet[releaseID] = true
	}
	for fileID, keys := range input.EvidenceCacheKeysByFile {
		if !memberIDs[fileID] || len(keys) == 0 {
			return prepared, fmt.Errorf("sealed matching evaluation response provenance has an invalid member")
		}
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return prepared, fmt.Errorf("sealed matching evaluation response provenance contains an empty key")
			}
			evidenceSet[key] = true
		}
	}
	for _, key := range input.ReleaseLookupCacheKeys {
		if strings.TrimSpace(key) == "" || !strings.HasPrefix(key, "release-lookup:") {
			return prepared, fmt.Errorf("sealed matching release lookup scope is invalid")
		}
		evidenceSet[key] = true
	}
	for _, key := range input.CatalogCacheKeys {
		if strings.TrimSpace(key) == "" {
			return prepared, fmt.Errorf("sealed matching catalog response scope is invalid")
		}
		if evidenceSet[key] {
			return prepared, fmt.Errorf("sealed matching response scope contains duplicate keys")
		}
		evidenceSet[key] = true
	}
	if input.SchemaVersion == 2 && len(evidenceSet) != len(input.ReleaseLookupCacheKeys)+len(input.CatalogCacheKeys) {
		return prepared, fmt.Errorf("sealed matching release lookup scope contains duplicates")
	}
	releaseKeys, evidenceKeys := sortedMatchingKeys(releaseSet), sortedMatchingKeys(evidenceSet)
	providerSnapshot, err := evaluator.provider.GetMusicBrainzMatchingSnapshot(ctx,
		musicBrainzEndpoint(config), config.Identity, releaseKeys, evidenceKeys)
	if err != nil {
		return prepared, err
	}
	if len(providerSnapshot.Releases) != len(releaseKeys) {
		return prepared, fmt.Errorf("sealed matching release graph is incomplete")
	}
	for index, release := range providerSnapshot.Releases {
		if release.ProviderKey != releaseKeys[index] || input.SchemaVersion == 2 && !release.Complete {
			return prepared, fmt.Errorf("sealed matching release graph differs from selected catalog identities")
		}
	}
	if input.SchemaVersion == 2 {
		for _, key := range input.ReleaseLookupCacheKeys {
			response := providerSnapshot.Responses[key]
			if response == nil || response.FetchedAt == nil || len(response.Payload) == 0 {
				return prepared, fmt.Errorf("sealed matching accepted release lookup projection is incomplete")
			}
		}
	}
	releasesByFile := make(map[uuid.UUID][]string, len(memberIDs))
	searchKeysByFile := make(map[uuid.UUID]MatchingSearchKeys, len(memberIDs))
	for id := range memberIDs {
		releasesByFile[id] = append([]string(nil), releaseKeys...)
		searchKeysByFile[id] = MatchingSearchKeys{}
	}
	results, err := EvaluateMatchingSnapshot(ctx, MatchingEvaluationSnapshot{
		Grouping: grouping, Captures: captures, Provider: providerSnapshot,
		RequestedGroupIDs: input.GroupIDs, TargetFenceVersion: targetFenceVersion(fence), ReleaseKeysByFile: releasesByFile, SearchKeysByFile: searchKeysByFile,
		allowMissingSearchKeys: true,
	})
	if err != nil {
		return prepared, err
	}
	for _, result := range results {
		if result.Decision == MatchingDecisionDeferred {
			prepared.Results = results
			return prepared, ErrMatchingEvaluationDeferred
		}
	}
	combined, err := combineMatchingGroupResults(results, providerSnapshot, releaseKeys, evidenceKeys)
	if err != nil {
		return prepared, err
	}
	combined.ScopeVersion = 4
	if fence != nil {
		combined.TargetFenceVersion = 2
		combined.TargetCaptureDigest = fence.CaptureDigest
	}
	prepared.Results, prepared.evaluation = results, combined
	return prepared, nil
}

func targetFenceVersion(fence *persistence.MatchingRunFenceSnapshot) int {
	if fence == nil {
		return 0
	}
	return fence.Version
}
