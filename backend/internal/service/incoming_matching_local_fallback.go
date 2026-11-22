package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// incomingMatchingOutageFallbackRepository is the narrow persistence boundary
// for the outage-specific Local fallback. The lookup is fenced by the explicit
// failure marker and the dedicated generation, never by provider health alone.
type incomingMatchingOutageFallbackRepository interface {
	CurrentMatchingMusicBrainzOutageFallback(context.Context, uuid.UUID, int64) (uuid.UUID, error)
	CaptureMatchingLayoutTarget(context.Context, uuid.UUID, string) (persistence.MatchingLayoutTarget, error)
	ApplyAutomaticLocalFallback(context.Context, persistence.AutomaticLocalFallbackCommit) (persistence.AutomaticLocalFallbackResult, error)
}

// incomingMatchingLocalLayoutCalculator is the shared whole-layout calculation
// over the read-only Local catalog. It never writes or grants authority.
type incomingMatchingLocalLayoutCalculator interface {
	CalculateLayout(context.Context, []persistence.IncomingGroupingCapture, float64) (MatchingLocalLayoutStage, error)
}

// WithLocalFallback enables the outage-specific Local fallback path in current
// reconciliation. Both parts are required: the fenced evidence/commit boundary
// and the read-only Local layout calculator.
func (service *IncomingMatchingReconciliation) WithLocalFallback(repository incomingMatchingOutageFallbackRepository, calculator incomingMatchingLocalLayoutCalculator) *IncomingMatchingReconciliation {
	service.localFallback = repository
	service.localCatalog = calculator
	return service
}

// reconcileOutageFallback handles one pending current generation that is exactly
// the dedicated generation written by a fenced MusicBrainz outage failure. It
// returns false when this generation has no outage-specific obligation and must
// therefore continue through ordinary reconciliation.
//
// A full usable Local layout is applied and acknowledged atomically. A proved
// absence (or an incomplete catalog layout, which the product contract sends to
// manual review) settles only the observed generation and admits nothing. No
// partial layout is ever written.
func (service *IncomingMatchingReconciliation) reconcileOutageFallback(ctx context.Context, groupID uuid.UUID, requestedGeneration int64) (bool, error) {
	if service.localFallback == nil || service.localCatalog == nil {
		return false, nil
	}
	outageRunID, err := service.localFallback.CurrentMatchingMusicBrainzOutageFallback(ctx, groupID, requestedGeneration)
	if err != nil {
		return true, fmt.Errorf("read MusicBrainz outage fallback evidence: %w", err)
	}
	if outageRunID == uuid.Nil {
		return false, nil
	}
	target, err := service.localFallback.CaptureMatchingLayoutTarget(ctx, groupID, MatchingLayoutPolicyVersion)
	if err != nil {
		if errors.Is(err, persistence.ErrMatchingTargetNeedsRefresh) || errors.Is(err, persistence.ErrIncomingMatchConflict) {
			return true, ErrIncomingMatchingReconciliationDeferred
		}
		return true, fmt.Errorf("capture outage fallback layout target: %w", err)
	}
	layout, err := service.localCatalog.CalculateLayout(ctx, target.Captures, target.Threshold)
	if err != nil {
		return true, fmt.Errorf("calculate outage fallback layout: %w", err)
	}
	if layout.Stage.Decision.Status != MatchingLayoutSelectionSelected {
		// No usable complete layout for the observed generation. Settle exactly
		// this generation and leave the group for manual review.
		if err := service.currentRequests.AcknowledgeCurrentRequest(ctx, groupID, requestedGeneration); err != nil {
			return true, fmt.Errorf("settle outage fallback layout for group %s: %w", groupID, err)
		}
		return true, nil
	}
	if layout.Stage.ProviderReleaseID == nil || *layout.Stage.ProviderReleaseID == uuid.Nil || len(layout.Stage.Assignments) == 0 {
		return true, fmt.Errorf("selected outage fallback layout is incomplete for group %s", groupID)
	}
	assignments := make([]persistence.MatchingLocalAssignment, 0, len(layout.Stage.Assignments))
	for _, assignment := range layout.Stage.Assignments {
		assignments = append(assignments, persistence.MatchingLocalAssignment(assignment))
	}
	_, err = service.localFallback.ApplyAutomaticLocalFallback(ctx, persistence.AutomaticLocalFallbackCommit{
		GroupID:                   groupID,
		OutageRunID:               outageRunID,
		ObservedRequestGeneration: requestedGeneration,
		ProviderReleaseID:         *layout.Stage.ProviderReleaseID,
		Assignments:               assignments,
		LayoutTarget:              target.Evidence,
		HeaderDigest:              layout.HeaderDigest,
		ReleaseIDs:                layout.ReleaseIDs,
		LayoutDigest:              layout.LayoutDigest,
	})
	if err != nil {
		if errors.Is(err, persistence.ErrIncomingMatchConflict) {
			return true, ErrIncomingMatchingReconciliationDeferred
		}
		return true, fmt.Errorf("apply automatic Local fallback for group %s: %w", groupID, err)
	}
	return true, nil
}
