package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingLocalLayoutStage binds the common layout decision to the exact Local
// catalog and shortlist projections it used. The digests must be checked again
// in the transaction that consumes the decision.
type MatchingLocalLayoutStage struct {
	Stage        MatchingLayoutStage
	HeaderDigest string
	ReleaseIDs   []uuid.UUID
	LayoutDigest string
}

// CalculateLayout ranks only headers, then hydrates at most the common
// shortlist limit. It neither changes the catalog nor grants write authority.
func (service *MatchingLocalCatalogSnapshotService) CalculateLayout(
	ctx context.Context, captures []persistence.IncomingGroupingCapture, threshold float64,
) (MatchingLocalLayoutStage, error) {
	members, err := MatchingLayoutRetrievalMembers(captures)
	if err != nil {
		return MatchingLocalLayoutStage{}, err
	}
	catalog, err := service.Headers(ctx)
	if err != nil {
		return MatchingLocalLayoutStage{}, err
	}
	shortlist, err := RankMatchingReleaseShortlist(members, catalog.ShortlistHeaders())
	if err != nil {
		return MatchingLocalLayoutStage{}, err
	}
	releaseByKey := make(map[string]uuid.UUID, len(catalog.Releases))
	for _, release := range catalog.Releases {
		releaseByKey[release.ID] = release.ReleaseID
	}
	releaseIDs := make([]uuid.UUID, len(shortlist))
	for i, key := range shortlist {
		releaseIDs[i] = releaseByKey[key]
		if releaseIDs[i] == uuid.Nil {
			return MatchingLocalLayoutStage{}, fmt.Errorf("shortlisted Local edition %q has no provider identity", key)
		}
	}
	var layouts MatchingLocalSelectionLayouts
	if len(releaseIDs) != 0 {
		layouts, err = service.Hydrate(ctx, releaseIDs)
		if err != nil {
			return MatchingLocalLayoutStage{}, err
		}
	}
	stage, err := CalculateMatchingLayoutStage(
		ctx, captures, shortlist, persistence.ProviderMatchingSnapshot{Releases: layouts.Releases}, nil, nil, threshold,
	)
	if err != nil {
		return MatchingLocalLayoutStage{}, err
	}
	return MatchingLocalLayoutStage{
		Stage: stage, HeaderDigest: catalog.Digest, ReleaseIDs: releaseIDs, LayoutDigest: layouts.Digest,
	}, nil
}
