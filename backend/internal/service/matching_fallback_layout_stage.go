package service

import (
	"context"
	"errors"
	"fmt"

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
	scope := persistence.MatchingFallbackProviderSource{
		ID: snapshot.RunSourceID, ProviderID: snapshot.RunProviderID,
		Endpoint: snapshot.RunEndpoint, ConfigurationIdentity: snapshot.RunConfiguration,
	}
	search := snapshot.SearchReceipt
	if snapshot.RunID == uuid.Nil || snapshot.AcquisitionRevision < 1 || snapshot.StageIndex < 0 ||
		snapshot.Intent.FallbackProviderSource == nil || *snapshot.Intent.FallbackProviderSource != scope ||
		scope.ID == uuid.Nil || scope.ProviderID == uuid.Nil || scope.Endpoint == "" || scope.ConfigurationIdentity == "" ||
		len(snapshot.Intent.Targets) != 1 || snapshot.Intent.Targets[0].GroupID != snapshot.Target.GroupID ||
		snapshot.Intent.Targets[0].Revision != snapshot.Target.GroupRevision ||
		snapshot.Intent.TargetCaptureDigest == "" || snapshot.Intent.TargetCaptureDigest != snapshot.Target.CaptureDigest ||
		search.RunID != snapshot.RunID || search.StageIndex != snapshot.StageIndex ||
		search.ProviderSourceID != scope.ID || search.ConfigurationIdentity != scope.ConfigurationIdentity ||
		search.CacheKey == "" || search.CacheKey != snapshot.SearchCacheKey ||
		!fallbackJSONEqual(search.RequestInput, snapshot.SearchInput) {
		return persistence.MatchingLayoutDecision{}, errors.New("fallback layout stage authority is invalid")
	}
	if err := shortlist.ValidateFallbackSearch(snapshot.SearchKind, search); err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	captures, threshold, err := persistence.DecodeMatchingLayoutTarget(snapshot.Intent.LayoutTarget, snapshot.Target.GroupID, MatchingLayoutPolicyVersion)
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	releases, err := fallbackLayoutAcceptedReleases(snapshot, scope, shortlist.RankedReleaseIDs)
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	stage, err := calculateMatchingLayoutStage(ctx, captures, shortlist.RankedReleaseIDs, releases, threshold, func() ([]MatchingInput, error) {
		return scoreFallbackLayoutReleases(ctx, captures, snapshot.SearchKind, search, releases)
	})
	if err != nil {
		return persistence.MatchingLayoutDecision{}, err
	}
	return stage.DurableDecision()
}

func fallbackLayoutAcceptedReleases(snapshot persistence.MatchingFallbackCalculationContext, scope persistence.MatchingFallbackProviderSource, shortlist []string) ([]persistence.MatchingRelease, error) {
	requests := make(map[uuid.UUID]persistence.MatchingFallbackCalculationLookupAuthority, len(shortlist))
	expected := make(map[string]bool, len(shortlist))
	for _, release := range shortlist {
		expected[release] = true
	}
	for _, lookup := range snapshot.Lookups {
		if !expected[lookup.ReleaseID] || lookup.RequestID == uuid.Nil || requests[lookup.RequestID].RequestID != uuid.Nil ||
			lookup.Kind != "provider_release_lookup" || lookup.StageIndex != snapshot.StageIndex ||
			lookup.CacheKey != "release-lookup:"+lookup.ReleaseID ||
			lookup.ProviderSourceID != scope.ID || lookup.ProviderID != scope.ProviderID ||
			lookup.Endpoint != scope.Endpoint || lookup.Configuration != scope.ConfigurationIdentity {
			return nil, errors.New("fallback layout stage has an off-scope or repeated lookup")
		}
		requests[lookup.RequestID] = lookup
		delete(expected, lookup.ReleaseID)
	}
	if len(expected) != 0 {
		return nil, errors.New("fallback layout stage lookup registrations do not cover shortlist")
	}
	releases := make([]persistence.MatchingRelease, 0, len(snapshot.LookupReceipts))
	for _, receipt := range snapshot.LookupReceipts {
		lookup, registered := requests[receipt.RequestID]
		if !registered || receipt.RunID != snapshot.RunID || receipt.StageIndex != snapshot.StageIndex ||
			receipt.CacheKey != lookup.CacheKey || !fallbackJSONEqual(lookup.Input, receipt.RequestInput) {
			return nil, errors.New("fallback layout stage receipt is outside registered shortlist")
		}
		release, _, err := persistence.DecodeFallbackLayoutLookup(receipt, scope, lookup.ReleaseID)
		if err != nil {
			return nil, fmt.Errorf("decode fallback layout lookup: %w", err)
		}
		releases = append(releases, release)
		delete(requests, receipt.RequestID)
	}
	// Missing receipts leave the projection incomplete; the layout stage
	// reports indeterminate rather than treating these as negative evidence.
	return releases, nil
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
