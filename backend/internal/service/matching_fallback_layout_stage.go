package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// CalculateFallbackLayoutDecision scores one registered artistless stage
// using its persisted target and immutable accepted lookup receipts. The
// shortlist is the hydration scope; the full accepted search universe is
// retained as evidence, not scored. No live provider reads are performed.
func CalculateFallbackLayoutDecision(ctx context.Context, snapshot persistence.MatchingFallbackCalculationContext, shortlist persistence.MatchingAcceptedShortlist) (persistence.MatchingLayoutDecision, error) {
	if err := ctx.Err(); err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	evidence, err := persistence.DecodeFallbackLayoutStageEvidence(snapshot, shortlist)
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	stage, err := calculateMatchingLayoutStage(ctx, evidence.Captures, shortlist.RankedReleaseIDs, evidence.Releases, evidence.PairThreshold, func() ([]MatchingInput, error) {
		return scoreFallbackLayoutReleases(ctx, evidence.Captures, snapshot.SearchKind, snapshot.SearchReceipt, evidence.Releases)
	})
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	decision, err := stage.DurableDecision()
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	if err := evidence.ValidateDecision(decision); err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	return decision, nil
}

func scoreFallbackLayoutReleases(ctx context.Context, captures []persistence.IncomingGroupingCapture, searchKind string, search persistence.MatchingFallbackSearchReceipt, releases []persistence.MatchingRelease) ([]MatchingInput, error) {
	key := MatchingSearchKeys{Release: search.CacheKey}
	if searchKind == "provider_recording_search" {
		key = MatchingSearchKeys{Recording: search.CacheKey}
	}
	keys := make(map[uuid.UUID]MatchingSearchKeys, len(captures))
	for _, capture := range captures {
		keys[capture.VariantID] = key
	}
	var edges []MatchingInput
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A recording hit contributes only to editions actually named by
		// that hit, not to every edition containing that recording.
		scores, err := acceptedFallbackSearchScores(search.CacheKey, searchKind, search.Payload, []string{release.ProviderKey})
		if err != nil {
			return nil, err
		}
		projection := persistence.ProviderMatchingSnapshot{
			Releases:     []persistence.MatchingRelease{release},
			SearchScores: scores,
		}
		pairs, err := BuildLayoutMatchingInputs(captures, projection, keys, nil)
		if err != nil {
			return nil, err
		}
		edges = append(edges, pairs...)
	}
	return edges, nil
}
