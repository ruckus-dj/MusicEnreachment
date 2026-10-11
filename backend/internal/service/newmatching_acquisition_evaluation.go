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
	var prepared PreparedMatchingEvaluation
	if evaluator == nil || evaluator.groups == nil || evaluator.captures == nil || evaluator.provider == nil || evaluator.results == nil || evaluator.config == nil {
		return prepared, fmt.Errorf("matching evaluation dependencies are unavailable")
	}
	if input.SchemaVersion != 1 || len(input.GroupIDs) == 0 || len(input.ReleaseIDs) > 10 {
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
	if len(input.EvidenceCacheKeysByFile) != len(memberIDs) {
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
		if release.ProviderKey != releaseKeys[index] {
			return prepared, fmt.Errorf("sealed matching release graph differs from selected catalog identities")
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
		RequestedGroupIDs: input.GroupIDs, ReleaseKeysByFile: releasesByFile, SearchKeysByFile: searchKeysByFile,
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
	prepared.Results, prepared.evaluation = results, combined
	return prepared, nil
}
