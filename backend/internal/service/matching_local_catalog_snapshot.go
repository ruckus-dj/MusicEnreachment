package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingLocalSelectionRepository is the narrow read-only Local selection
// boundary. It exposes header facts and targeted normalized release layouts.
type MatchingLocalSelectionRepository interface {
	ListMatchingLocalSelectionHeaders(context.Context) (persistence.MatchingLocalSelectionCatalog, error)
	HydrateMatchingLocalSelection(context.Context, []uuid.UUID) (persistence.MatchingLocalSelectionSnapshot, error)
}

// MatchingLocalSelectionRelease is one Local header projected onto the shared
// retrieval candidate shape. ReleaseID addresses hydration; ProviderKey is the
// stable release identity used by matching.
type MatchingLocalSelectionRelease struct {
	ReleaseID uuid.UUID
	MatchingRetrievalCandidate
	PositionCount int
}

// MatchingLocalSelectionCatalog is the ranked-candidate source for Local.
// Digest covers the same header evidence that later apply validation must see.
type MatchingLocalSelectionCatalog struct {
	Releases []MatchingLocalSelectionRelease
	Digest   string
}

// ShortlistHeader projects one Local release onto the shared pure shortlist
// header shape. Local always knows its position count, so the shared optional
// count is present rather than unknown.
func (release MatchingLocalSelectionRelease) ShortlistHeader() MatchingReleaseHeader {
	count := release.PositionCount
	return MatchingReleaseHeader{
		MatchingRetrievalCandidate: release.MatchingRetrievalCandidate,
		PositionCount:              &count,
	}
}

// ShortlistHeaders returns the Local candidate set in the shared public header
// shape accepted by RankMatchingReleaseShortlist. It performs no additional
// reads: the evidence is already in the loaded header snapshot.
func (catalog MatchingLocalSelectionCatalog) ShortlistHeaders() []MatchingReleaseHeader {
	headers := make([]MatchingReleaseHeader, 0, len(catalog.Releases))
	for _, release := range catalog.Releases {
		headers = append(headers, release.ShortlistHeader())
	}
	return headers
}

// MatchingLocalSelectionLayouts contains normalized full layouts for only the
// explicitly selected releases, plus their stable evidence digest.
type MatchingLocalSelectionLayouts struct {
	Releases []persistence.MatchingRelease
	Digest   string
}

// MatchingLocalCatalogSnapshotService maps the read-only Local persistence
// adapter onto matching retrieval types. It never applies a layout or writes.
type MatchingLocalCatalogSnapshotService struct {
	repository MatchingLocalSelectionRepository
}

// NewMatchingLocalCatalogSnapshotService wires the read-only Local adapter.
func NewMatchingLocalCatalogSnapshotService(repository MatchingLocalSelectionRepository) *MatchingLocalCatalogSnapshotService {
	return &MatchingLocalCatalogSnapshotService{repository: repository}
}

// Headers returns cheap Local release headers for whole-catalog ranking. It
// performs no recording hydration.
func (service *MatchingLocalCatalogSnapshotService) Headers(ctx context.Context) (MatchingLocalSelectionCatalog, error) {
	if service == nil || service.repository == nil {
		return MatchingLocalSelectionCatalog{}, errors.New("local matching selection is unavailable")
	}
	catalog, err := service.repository.ListMatchingLocalSelectionHeaders(ctx)
	if err != nil {
		return MatchingLocalSelectionCatalog{}, err
	}
	result := MatchingLocalSelectionCatalog{
		Releases: make([]MatchingLocalSelectionRelease, 0, len(catalog.Headers)),
		Digest:   catalog.Digest,
	}
	for _, header := range catalog.Headers {
		result.Releases = append(result.Releases, matchingLocalSelectionRelease(header))
	}
	return result, nil
}

// Hydrate returns normalized layouts for exactly the selected release IDs.
func (service *MatchingLocalCatalogSnapshotService) Hydrate(ctx context.Context, releaseIDs []uuid.UUID) (MatchingLocalSelectionLayouts, error) {
	if service == nil || service.repository == nil {
		return MatchingLocalSelectionLayouts{}, errors.New("local matching selection is unavailable")
	}
	snapshot, err := service.repository.HydrateMatchingLocalSelection(ctx, releaseIDs)
	if err != nil {
		return MatchingLocalSelectionLayouts{}, err
	}
	return MatchingLocalSelectionLayouts{
		Releases: append([]persistence.MatchingRelease(nil), snapshot.Releases...),
		Digest:   snapshot.Digest,
	}, nil
}

func matchingLocalSelectionRelease(header persistence.MatchingLocalSelectionHeader) MatchingLocalSelectionRelease {
	credits := make([]string, 0, len(header.Credits))
	var caption strings.Builder
	for _, credit := range header.Credits {
		if credit.Name == "" {
			continue
		}
		credits = append(credits, credit.Name)
		caption.WriteString(credit.Name)
		caption.WriteString(credit.NameJoinPhrase)
	}
	return MatchingLocalSelectionRelease{
		ReleaseID: header.ReleaseID,
		MatchingRetrievalCandidate: MatchingRetrievalCandidate{
			ID:             header.ProviderKey,
			Title:          header.Title,
			Disambiguation: header.Disambiguation,
			Date:           header.ReleaseDate,
			ArtistCredit:   caption.String(),
			ArtistCredits:  credits,
			CatalogNumbers: append([]string(nil), header.CatalogNumbers...),
		},
		PositionCount: header.PositionCount,
	}
}
