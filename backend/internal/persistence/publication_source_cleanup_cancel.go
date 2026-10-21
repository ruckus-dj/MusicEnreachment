package persistence

import "context"

// CancelUnstartedPublicationSourceCleanupDelivery preserves the disable-only
// cancellation contract while sharing the exact retirement proof.
func (repository *SetupManagerRepository) CancelUnstartedPublicationSourceCleanupDelivery(ctx context.Context, guard PublicationSourceCleanupDeliveryGuard, reason string) (bool, error) {
	return repository.retirePublicationSourceCleanupDelivery(ctx, guard, reason, retirePublicationSourceCleanupDisabled)
}
