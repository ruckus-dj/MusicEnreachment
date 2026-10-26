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
	EvaluatePublicationReconciliationCurrent(context.Context, uuid.UUID, persistence.PublicationReconciliationDeliveryGuard, string, string) (persistence.PublicationReconciliationDecision, error)
	AcknowledgePublicationReconciliationNoop(context.Context, persistence.PublicationReconciliationDeliveryGuard, *persistence.PublicationReconciliationNoop) (bool, error)
}

type publicationReconciliationBusyParker interface {
	ParkPublicationReconciliationBusyAttempt(context.Context, persistence.PublicationReconciliationDeliveryGuard, int64, string) (bool, error)
}

type publicationReconciliationInputsPendingParker interface {
	ParkPublicationReconciliationInputsPending(context.Context, *persistence.PublicationReconciliationInputsPending) (bool, error)
}

// ReconcilePublication consumes one already-requested publication reconciliation.
// The worker owns claim release on every error; nil means this delivery was
// transactionally parked, admitted, or explicitly acknowledged as a no-op.
//
// A retained attempt is parked only after persistence atomically revalidates
// both the delivery and the busy condition. For CURRENT publications, nil means
// the delivery was admitted, safely parked, or explicitly acknowledged as a
// proven no-op.
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
		parked, err := parkBusyReconciliation(ctx, service.repository, guard, facts.RequestedGeneration)
		if err != nil {
			return fmt.Errorf("park reconciliation with retained publication attempt: %w", err)
		}
		if !parked {
			return fmt.Errorf("reconcile publication: retained-attempt condition changed; retry delivery")
		}
		return nil
	}
	if facts.HasCurrentPublication {
		return service.reconcileCurrentPublication(ctx, repository, facts.ReleaseID, guard)
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
		return service.reconcileCurrentPublication(ctx, repository, facts.ReleaseID, guard)
	}
	if snapshot.Library.ActiveAttempt != nil {
		parked, err := parkBusyReconciliation(ctx, service.repository, guard, facts.RequestedGeneration)
		if err != nil {
			return fmt.Errorf("park reconciliation with retained publication attempt: %w", err)
		}
		if !parked {
			return fmt.Errorf("reconcile publication: retained-attempt condition changed; retry delivery")
		}
		return nil
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

func (service *LibraryPublications) reconcileCurrentPublication(ctx context.Context, repository publicationReconciliationRepository, releaseID uuid.UUID, guard persistence.PublicationReconciliationDeliveryGuard) error {
	endpoint, configuration := "", ""
	if reader, ok := service.settings.(matchingConfigurationReader); ok {
		config, err := reader.GetMusicBrainzConfig(ctx)
		if err != nil {
			return fmt.Errorf("read MusicBrainz configuration for publication reconciliation: %w", err)
		}
		endpoint, configuration = musicBrainzEndpoint(config), config.Identity
	}
	if service.coordinator == nil {
		return fmt.Errorf("reconcile current publication: publication coordinator is unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return fmt.Errorf("acquire publication reconciliation coordinator: %w", err)
	}
	decision, err := repository.EvaluatePublicationReconciliationCurrent(ctx, releaseID, guard, endpoint, configuration)
	unlock()
	if err != nil {
		return fmt.Errorf("evaluate current publication reconciliation: %w", err)
	}
	switch decision.Action {
	case persistence.AudioUpgradeRequired:
		operationID, err := service.admitAutomaticAudioUpgrade(ctx, releaseID, guard.PositionID, guard)
		if err != nil {
			return fmt.Errorf("admit automatic audio upgrade: %w", err)
		}
		if operationID == uuid.Nil {
			return fmt.Errorf("reconcile current publication: audio-upgrade admission returned no operation; retry delivery")
		}
		return nil
	case persistence.ProviderTagsRequired:
		operationID, err := service.admitAutomaticProviderTags(ctx, releaseID, guard.PositionID, guard)
		if err != nil {
			return fmt.Errorf("admit automatic provider tags: %w", err)
		}
		if operationID == uuid.Nil {
			return fmt.Errorf("reconcile current publication: provider-tag condition changed during admission; retry delivery")
		}
		return nil
	case persistence.AllNoop:
		if decision.Noop == nil {
			return fmt.Errorf("reconcile current publication: all-noop decision has no witness")
		}
		unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
		if err != nil {
			return fmt.Errorf("acquire publication no-op acknowledgement coordinator: %w", err)
		}
		acknowledged, ackErr := repository.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop)
		unlock()
		if ackErr != nil {
			return fmt.Errorf("acknowledge current publication no-op: %w", ackErr)
		}
		if !acknowledged {
			return fmt.Errorf("reconcile current publication: no-op proof changed before acknowledgement; retry delivery")
		}
		return nil
	case persistence.InputsPending:
		if decision.InputsPending == nil {
			return fmt.Errorf("reconcile current publication: pending-input decision has no witness")
		}
		parker, ok := service.repository.(publicationReconciliationInputsPendingParker)
		if !ok {
			return fmt.Errorf("reconcile current publication: pending-input parking is unavailable")
		}
		parked, err := parker.ParkPublicationReconciliationInputsPending(ctx, decision.InputsPending)
		if err != nil {
			return fmt.Errorf("park publication reconciliation with pending accepted graph: %w", err)
		}
		if !parked {
			return fmt.Errorf("reconcile current publication: pending-input proof changed before parking; retry delivery")
		}
		return nil
	default:
		return fmt.Errorf("reconcile current publication: invalid reconciliation action %d", decision.Action)
	}
}

func parkBusyReconciliation(ctx context.Context, candidate any, guard persistence.PublicationReconciliationDeliveryGuard, generation int64) (bool, error) {
	parker, ok := candidate.(publicationReconciliationBusyParker)
	if !ok {
		return false, fmt.Errorf("reconcile publication: busy-attempt parking is unavailable")
	}
	return parker.ParkPublicationReconciliationBusyAttempt(ctx, guard, generation, persistence.PublicationReconciliationBusyAttemptReason)
}
