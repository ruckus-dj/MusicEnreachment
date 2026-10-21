package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type sourceCleanupRetryRepository interface {
	ReadPublicationSourceCleanupExplicitRetryState(context.Context, uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryState, error)
	AdmitPublicationSourceCleanupExplicitRetry(context.Context, uuid.UUID, persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error)
	ReplacePublicationSourceCleanupExplicitRetry(context.Context, uuid.UUID, uuid.UUID, persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error)
}

// SourceCleanupRetries is the explicit-user boundary for publication source
// cleanup retries. It is not called by generic operation retry or job paths.
type SourceCleanupRetries struct {
	repository sourceCleanupRetryRepository
	river      persistence.RiverInserter
}

func NewSourceCleanupRetries(repository sourceCleanupRetryRepository, river persistence.RiverInserter) *SourceCleanupRetries {
	return &SourceCleanupRetries{repository: repository, river: river}
}

func (service *SourceCleanupRetries) Retry(ctx context.Context, originalOperationID uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryResult, error) {
	if ctx == nil || originalOperationID == uuid.Nil {
		return persistence.PublicationSourceCleanupExplicitRetryResult{}, fmt.Errorf("retry publication source cleanup: valid context and original operation are required")
	}
	if service == nil || service.repository == nil || service.river == nil {
		return persistence.PublicationSourceCleanupExplicitRetryResult{}, errors.New("retry publication source cleanup: service is unavailable")
	}
	state, err := service.repository.ReadPublicationSourceCleanupExplicitRetryState(ctx, originalOperationID)
	if err != nil {
		return persistence.PublicationSourceCleanupExplicitRetryResult{}, fmt.Errorf("retry publication source cleanup: read retry state: %w", err)
	}
	if state.OperationID != originalOperationID || state.Kind != "cleanup_publication_sources_v1" {
		return persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: []string{"original operation is not a publication source cleanup"}}, nil
	}
	if state.State != "failed" || state.Stage != "cleanup_recovery_required" || !state.SnapshotValid {
		return persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: []string{"original cleanup is not in a valid recovery-required state"}}, nil
	}
	if state.CurrentGrant {
		if state.RecoveryOperationID == uuid.Nil || state.RecoveryKind != "retry_publication_sources_v1" || !state.RecoverySnapshotValid || !state.RecoveryGrantValid {
			return persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: []string{"current cleanup retry grant is incomplete"}}, nil
		}
		if state.RecoveryState != "failed" || state.RecoveryStage != "cleanup_retry_recovery_required" || state.RecoveryClaimState != "recovery_required" || !state.RecoveryFrontier {
			return persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: []string{"current cleanup retry is still active or has not returned for recovery"}}, nil
		}
		result, err := service.repository.ReplacePublicationSourceCleanupExplicitRetry(ctx, originalOperationID, state.RecoveryOperationID, service.river)
		if err != nil {
			return persistence.PublicationSourceCleanupExplicitRetryResult{}, fmt.Errorf("replace publication source cleanup retry: %w", err)
		}
		return result, nil
	}
	if state.RecoveryFrontier {
		return persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: []string{"cleanup recovery frontier exists without a current retry grant"}}, nil
	}
	result, err := service.repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, service.river)
	if err != nil {
		return persistence.PublicationSourceCleanupExplicitRetryResult{}, fmt.Errorf("admit publication source cleanup retry: %w", err)
	}
	return result, nil
}
