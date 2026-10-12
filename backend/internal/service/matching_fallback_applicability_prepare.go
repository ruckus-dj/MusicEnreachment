package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingFallbackCalculationContextReader interface {
	LoadMatchingFallbackCalculationContext(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackCalculationContext, bool, error)
}

// PrepareFallbackApplicability loads one immutable persistence snapshot and
// passes an explicit service-owned conversion to the pure calculator.
func PrepareFallbackApplicability(
	ctx context.Context,
	reader matchingFallbackCalculationContextReader,
	runID uuid.UUID,
	expectedRevision int64,
) (MatchingFallbackApplicabilityResult, bool, error) {
	if reader == nil {
		return MatchingFallbackApplicabilityResult{}, false, fmt.Errorf("fallback applicability: context reader is unavailable")
	}
	snapshot, exists, err := reader.LoadMatchingFallbackCalculationContext(ctx, runID, expectedRevision)
	if err != nil || !exists {
		return MatchingFallbackApplicabilityResult{}, exists, err
	}
	lookups := make([]MatchingFallbackLookupAuthority, len(snapshot.Lookups))
	for index, lookup := range snapshot.Lookups {
		lookups[index] = MatchingFallbackLookupAuthority{
			RequestID: lookup.RequestID, ReleaseID: lookup.ReleaseID, Kind: lookup.Kind,
			ProviderSourceID: lookup.ProviderSourceID, ProviderID: lookup.ProviderID,
			Endpoint: lookup.Endpoint, Configuration: lookup.Configuration,
			CacheKey: lookup.CacheKey, Input: append([]byte(nil), lookup.Input...),
		}
	}
	input := MatchingFallbackApplicabilityInput{
		RunID: snapshot.RunID, AcquisitionRevision: snapshot.AcquisitionRevision,
		Intent: snapshot.Intent, Target: snapshot.Target,
		PhaseInput: append([]byte(nil), snapshot.PhaseInput...), PhaseOutput: append([]byte(nil), snapshot.PhaseOutput...),
		PhaseProvenance: append([]byte(nil), snapshot.PhaseProvenance...), SearchReceipt: snapshot.SearchReceipt,
		LookupReceipts: snapshot.LookupReceipts,
		Authority: MatchingFallbackAuthority{
			RunSourceID: snapshot.RunSourceID, RunProviderID: snapshot.RunProviderID,
			RunEndpoint: snapshot.RunEndpoint, RunConfiguration: snapshot.RunConfiguration,
			SearchKind: snapshot.SearchKind, SearchCacheKey: snapshot.SearchCacheKey,
			SearchInput: append([]byte(nil), snapshot.SearchInput...), Lookups: lookups,
		},
	}
	result, err := CalculateFallbackApplicability(input)
	return result, true, err
}
