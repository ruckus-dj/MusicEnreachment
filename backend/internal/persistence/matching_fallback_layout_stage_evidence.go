package persistence

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// MatchingFallbackLayoutStageEvidence is the scoring projection of a saved
// stage. Releases contains only accepted lookups for the ranked shortlist.
// Missing receipts are permitted in the projection but do not prove a negative.
type MatchingFallbackLayoutStageEvidence struct {
	Captures      []IncomingGroupingCapture
	PairThreshold float64
	Releases      []MatchingRelease
	lookupCount   int
}

func DecodeFallbackLayoutStageEvidence(snapshot MatchingFallbackCalculationContext, shortlist MatchingAcceptedShortlist) (MatchingFallbackLayoutStageEvidence, error) {
	scope := MatchingFallbackProviderSource{
		ID: snapshot.RunSourceID, ProviderID: snapshot.RunProviderID,
		Endpoint: snapshot.RunEndpoint, ConfigurationIdentity: snapshot.RunConfiguration,
	}
	search, target, intent := snapshot.SearchReceipt, snapshot.Target, snapshot.Intent
	if snapshot.RunID == uuid.Nil || snapshot.AcquisitionRevision < 1 || snapshot.StageIndex < 0 ||
		intent.SchemaVersion != 3 || intent.FenceVersion != 2 || target.Version != 2 ||
		intent.FallbackProviderSource == nil || *intent.FallbackProviderSource != scope ||
		scope.ID == uuid.Nil || scope.ProviderID == uuid.Nil || scope.Endpoint == "" || scope.ConfigurationIdentity == "" ||
		len(intent.Targets) != 1 || intent.Targets[0] != (MatchingTarget{GroupID: target.GroupID, Revision: target.GroupRevision}) ||
		intent.TargetCaptureDigest == "" || intent.TargetCaptureDigest != target.CaptureDigest ||
		search.RunID != snapshot.RunID || search.StageIndex != snapshot.StageIndex ||
		search.ProviderSourceID != scope.ID || search.ConfigurationIdentity != scope.ConfigurationIdentity ||
		search.CacheKey == "" || search.CacheKey != snapshot.SearchCacheKey ||
		!matchingJSONEqual(search.RequestInput, snapshot.SearchInput) {
		return MatchingFallbackLayoutStageEvidence{}, errors.New("fallback layout stage authority is invalid")
	}
	if err := shortlist.ValidateFallbackSearch(snapshot.SearchKind, search); err != nil {
		return MatchingFallbackLayoutStageEvidence{}, err
	}
	captures, threshold, err := DecodeMatchingLayoutTarget(intent.LayoutTarget, target.GroupID, MatchingLayoutPolicyVersion)
	if err != nil {
		return MatchingFallbackLayoutStageEvidence{}, err
	}
	releases, err := fallbackLayoutAcceptedReleases(snapshot, scope, shortlist.RankedReleaseIDs)
	if err != nil {
		return MatchingFallbackLayoutStageEvidence{}, err
	}
	return MatchingFallbackLayoutStageEvidence{
		Captures: captures, PairThreshold: threshold, Releases: releases, lookupCount: len(shortlist.RankedReleaseIDs),
	}, nil
}

// ValidateDecision checks a result against this independently decoded scope. It
// does not repeat the selection algorithm or establish database freshness.
func (evidence MatchingFallbackLayoutStageEvidence) ValidateDecision(decision MatchingLayoutDecision) error {
	if decision.Status != MatchingLayoutDecisionIndeterminate && len(evidence.Releases) != evidence.lookupCount {
		return errors.New("fallback layout cannot be decided with missing lookup receipts")
	}
	seen := make(map[uuid.UUID]bool, len(evidence.Captures))
	memberIDs := make([]uuid.UUID, 0, len(evidence.Captures))
	for _, capture := range evidence.Captures {
		if !seen[capture.VariantID] {
			memberIDs = append(memberIDs, capture.VariantID)
			seen[capture.VariantID] = true
		}
	}
	return decision.Validate(MatchingLayoutPolicyVersion, evidence.PairThreshold, memberIDs, evidence.Releases)
}

func fallbackLayoutAcceptedReleases(snapshot MatchingFallbackCalculationContext, scope MatchingFallbackProviderSource, shortlist []string) ([]MatchingRelease, error) {
	requests := make(map[uuid.UUID]MatchingFallbackCalculationLookupAuthority, len(shortlist))
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
	byRelease := make(map[string]MatchingRelease, len(snapshot.LookupReceipts))
	for _, receipt := range snapshot.LookupReceipts {
		lookup, registered := requests[receipt.RequestID]
		if !registered || receipt.RunID != snapshot.RunID || receipt.StageIndex != snapshot.StageIndex ||
			receipt.CacheKey != lookup.CacheKey || !matchingJSONEqual(lookup.Input, receipt.RequestInput) {
			return nil, errors.New("fallback layout stage receipt is outside registered shortlist")
		}
		release, _, err := DecodeFallbackLayoutLookup(receipt, scope, lookup.ReleaseID)
		if err != nil {
			return nil, fmt.Errorf("decode fallback layout lookup: %w", err)
		}
		byRelease[lookup.ReleaseID] = release
		delete(requests, receipt.RequestID)
	}
	releases := make([]MatchingRelease, 0, len(byRelease))
	for _, key := range shortlist {
		if release, ok := byRelease[key]; ok {
			releases = append(releases, release)
		}
	}
	return releases, nil
}
