package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PrepareFirstCover performs at most one artwork preparation for an eligible
// first publication position. A position with CURRENT or an active attempt is a
// no-op. Manual cover selections retain priority, but their exact MusicBrainz
// edition still prepares gallery originals so a later publication can use the
// persisted front or saved local source. This method is the explicit
// preparation step for read-only previews: preview and admission planning never
// fetch artwork themselves.
func (service *LibraryPublications) PrepareFirstCover(ctx context.Context, releaseID, positionID uuid.UUID) error {
	_, err := service.prepareFirstCover(ctx, releaseID, positionID)
	return err
}

// prepareFirstCover captures the position state, prepares artwork outside any
// admission transaction, then recaptures the context so a caller can plan
// against the authority it just froze.
func (service *LibraryPublications) prepareFirstCover(ctx context.Context, releaseID, positionID uuid.UUID) (*persistence.LibraryFirstPublicationContext, error) {
	if service == nil || service.repository == nil {
		return nil, errors.New("first publication cover preparation is unavailable")
	}
	if releaseID == uuid.Nil || positionID == uuid.Nil {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return nil, errors.New("first publication context is unavailable")
	}
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return nil, fmt.Errorf("read first publication cover eligibility: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil {
		return nil, errors.New("first publication context is incomplete")
	}
	if service.artwork == nil || !firstPublicationCoverPreparationEligible(snapshot) {
		return snapshot, nil
	}
	if err := service.preparePublicationArtwork(ctx, releaseID); err != nil {
		return nil, err
	}
	refreshed, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return nil, fmt.Errorf("read prepared first publication cover context: %w", err)
	}
	return refreshed, nil
}

// firstPublicationCoverPreparationEligible is deliberately narrow: only an
// ordinary never-published position may trigger artwork work. CURRENT rewrites
// are always excluded. Manual selections are included because gallery originals
// must already be prepared for the exact edition; the primary service keeps the
// manual authority and never replaces it with a default.
func firstPublicationCoverPreparationEligible(snapshot *persistence.LibraryFirstPublicationContext) bool {
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current != nil || snapshot.Library.ActiveAttempt != nil {
		return false
	}
	return true
}
