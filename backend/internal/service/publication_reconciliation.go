package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationReconciliationRepository interface {
	ResolvePublicationReconciliationContext(context.Context, persistence.PublicationReconciliationDeliveryGuard) (*persistence.PublicationReconciliationContext, error)
}

// ReconcilePublication consumes one already-requested publication reconciliation.
// The worker owns claim release on every error; nil means this delivery admitted
// and transactionally acknowledged.
//
// The current-publication and quality facts below come from snapshots read
// outside the admission transaction, while not every invalidation producer
// (settings, source, and manual changes) is wired to re-run reconciliation.
// Permanent parking is therefore blocked here until exact transactional
// revalidation and the complete wake frontier exist: an unsatisfied runtime
// decision is returned as a contextual retryable error, and the worker's
// existing owned-release queues plus River backoff preserve the intent. This is
// technical safe scheduling, not a new source policy. The generic parking
// persistence API stays available, unused here, for that future path.
func (service *LibraryPublications) ReconcilePublication(ctx context.Context, guard persistence.PublicationReconciliationDeliveryGuard) error {
	if service == nil || service.repository == nil {
		return fmt.Errorf("reconcile publication: service is unavailable")
	}
	repository, ok := service.repository.(publicationReconciliationRepository)
	if !ok {
		return fmt.Errorf("reconcile publication: reconciliation repository is unavailable")
	}
	facts, err := repository.ResolvePublicationReconciliationContext(ctx, guard)
	if err != nil {
		return fmt.Errorf("resolve publication reconciliation context: %w", err)
	}
	if facts == nil || facts.ReleaseID == uuid.Nil || facts.RequestedGeneration <= 0 {
		return fmt.Errorf("reconcile publication: reconciliation context is incomplete")
	}
	if facts.HasActiveAttempt {
		return fmt.Errorf("reconcile publication: a publication attempt is retained")
	}
	if facts.HasCurrentPublication {
		if _, err := service.admitAutomaticAudioUpgrade(ctx, facts.ReleaseID, guard.PositionID, guard); err != nil {
			return fmt.Errorf("admit automatic audio upgrade: %w", err)
		}
		return nil
	}

	contextReader, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return fmt.Errorf("reconcile publication: first publication context is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return fmt.Errorf("reconcile publication: publication planning settings are unavailable")
	}
	snapshot, err := contextReader.ReadLibraryFirstPublicationContext(ctx, facts.ReleaseID, guard.PositionID)
	if err != nil {
		return fmt.Errorf("read first publication context: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.ActiveEpoch != guard.OutputEpoch {
		return fmt.Errorf("reconcile publication: first publication context changed")
	}
	if snapshot.Library.Current != nil {
		if _, err := service.admitAutomaticAudioUpgrade(ctx, facts.ReleaseID, guard.PositionID, guard); err != nil {
			return fmt.Errorf("admit automatic audio upgrade: %w", err)
		}
		return nil
	}
	if snapshot.Library.ActiveAttempt != nil {
		return fmt.Errorf("reconcile publication: a publication attempt is retained")
	}
	if service.coverAcquisition != nil {
		if err := service.coverAcquisition.Ensure(ctx, facts.ReleaseID); err != nil {
			return fmt.Errorf("acquire release cover before first publication: %w", err)
		}
	}

	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return fmt.Errorf("read publication planning settings: %w", err)
	}
	if _, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings); err != nil {
		return fmt.Errorf("select verified publication tools: %w", err)
	}
	_, reason, err := automaticFirstSourceChoiceWithReason(snapshot, publication.AudioPublicationFormat(runtimeSettings.PublicationFormat))
	if err != nil {
		if reason == "" {
			return fmt.Errorf("select verified publication source: %w", err)
		}
		return fmt.Errorf("reconcile publication: automatic source selection is unsatisfied (%s) until reconciliation revalidates it", reason)
	}
	if _, err := service.admitAutomaticFirstPublication(ctx, facts.ReleaseID, guard.PositionID, guard); err != nil {
		return fmt.Errorf("admit automatic first publication: %w", err)
	}
	return nil
}
