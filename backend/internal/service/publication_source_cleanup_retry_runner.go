package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationSourceCleanupExplicitRetryRepository interface {
	LoadPublicationSourceCleanupExplicitRetryDelivery(context.Context, uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryDeliveryInput, error)
	ClaimPublicationSourceCleanupExplicitRetryDelivery(context.Context, persistence.PublicationSourceCleanupDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error)
	ResolvePublicationSourceCleanupRetryItem(context.Context, persistence.PublicationSourceCleanupDeliveryGuard, uuid.UUID, func(context.Context, persistence.PublicationSourceCleanupRemovalRequest) (persistence.PublicationSourceCleanupNativeOutcome, error)) (persistence.PublicationSourceCleanupRetryResolutionOutcome, error)
	SettlePublicationSourceCleanupExplicitRetry(context.Context, persistence.PublicationSourceCleanupDeliveryGuard) (bool, error)
	FailPublicationSourceCleanupExplicitRetryDelivery(context.Context, persistence.PublicationSourceCleanupDeliveryGuard, string) (bool, error)
}

type publicationSourceCleanupOutcomeRemover interface {
	RemoveWithOutcome(context.Context, string, string, int64, time.Time) (sourcefs.SourceRemovalOutcome, error)
}

// PublicationSourceCleanupExplicitRetryRunner executes only an explicitly
// admitted retry, with a fresh claim unrelated to the original delivery.
type PublicationSourceCleanupExplicitRetryRunner struct {
	repository publicationSourceCleanupExplicitRetryRepository
	remover    publicationSourceCleanupOutcomeRemover
	bootUUID   uuid.UUID
}

func NewPublicationSourceCleanupExplicitRetryRunner(repository publicationSourceCleanupExplicitRetryRepository, remover publicationSourceCleanupOutcomeRemover, bootUUID uuid.UUID) (*PublicationSourceCleanupExplicitRetryRunner, error) {
	if repository == nil || remover == nil || bootUUID == uuid.Nil {
		return nil, fmt.Errorf("explicit publication source cleanup retry runner requires repository, remover, and boot identity")
	}
	return &PublicationSourceCleanupExplicitRetryRunner{repository: repository, remover: remover, bootUUID: bootUUID}, nil
}

func (runner *PublicationSourceCleanupExplicitRetryRunner) ExecutePublicationSourceCleanupExplicitRetry(ctx context.Context, operationID uuid.UUID, riverJobID int64, riverAttempt int) (PublicationSourceCleanupRunOutcome, error) {
	if ctx == nil || ctx.Err() != nil || operationID == uuid.Nil || riverJobID <= 0 || riverAttempt <= 0 {
		return PublicationSourceCleanupBlocked, fmt.Errorf("execute explicit publication source cleanup retry: valid live context and delivery identity are required")
	}
	input, err := runner.repository.LoadPublicationSourceCleanupExplicitRetryDelivery(ctx, operationID)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("load explicit publication source cleanup retry: %w", err)
	}
	if input.OperationID != operationID || input.ApplicationAttempt <= 0 || input.Epoch == uuid.Nil || input.PositionID == uuid.Nil || len(input.ItemIDs) == 0 {
		return PublicationSourceCleanupBlocked, fmt.Errorf("load explicit publication source cleanup retry: invalid delivery input")
	}
	claimID, err := uuid.NewRandom()
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("create explicit publication source cleanup retry claim identity: %w", err)
	}
	tokenID, err := uuid.NewRandom()
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("create explicit publication source cleanup retry delivery token: %w", err)
	}
	guard := persistence.PublicationSourceCleanupDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: input.ApplicationAttempt, RiverJobID: riverJobID,
		RiverAttempt: riverAttempt, ClaimUUID: claimID, BootUUID: runner.bootUUID,
		DeliveryToken: tokenID.String(), Epoch: input.Epoch, PositionID: input.PositionID,
	}
	claim, err := runner.repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("claim explicit publication source cleanup retry delivery: %w", err)
	}
	if claim != persistence.PublicationDeliveryAcquired {
		return PublicationSourceCleanupBlocked, nil
	}
	for _, itemID := range input.ItemIDs {
		resolution, resolveErr := runner.repository.ResolvePublicationSourceCleanupRetryItem(ctx, guard, itemID, func(ctx context.Context, request persistence.PublicationSourceCleanupRemovalRequest) (persistence.PublicationSourceCleanupNativeOutcome, error) {
			outcome, err := runner.remover.RemoveWithOutcome(ctx, request.ConfiguredRootPath, request.RelativePath, request.SizeBytes, request.MTime)
			switch {
			case outcome == sourcefs.SourceRemovalRemoved && err == nil:
				return persistence.PublicationSourceCleanupNativeRemoved, nil
			case outcome == sourcefs.SourceRemovalMissingLeaf && errors.Is(err, fs.ErrNotExist):
				return persistence.PublicationSourceCleanupNativeMissingLeaf, err
			default:
				if err == nil {
					err = fmt.Errorf("native source remover returned inconsistent outcome %d", outcome)
				}
				return persistence.PublicationSourceCleanupNativeUnresolved, err
			}
		})
		if resolveErr != nil {
			return runner.failAfterStop(ctx, guard, fmt.Errorf("resolve explicit publication source cleanup retry item: %w", resolveErr))
		}
		if resolution != persistence.PublicationSourceCleanupRetryResolutionCompleted && resolution != persistence.PublicationSourceCleanupRetryResolutionAlreadyCompleted {
			return runner.failAfterStop(ctx, guard, fmt.Errorf("resolve explicit publication source cleanup retry item: blocked for item %s", itemID))
		}
	}
	settled, err := runner.repository.SettlePublicationSourceCleanupExplicitRetry(ctx, guard)
	if err != nil {
		return runner.failAfterStop(ctx, guard, fmt.Errorf("settle explicit publication source cleanup retry: %w", err))
	}
	if !settled {
		return runner.failAfterStop(ctx, guard, fmt.Errorf("settle explicit publication source cleanup retry: settlement was not acknowledged"))
	}
	return PublicationSourceCleanupCompleted, nil
}

func (runner *PublicationSourceCleanupExplicitRetryRunner) failAfterStop(ctx context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard, cause error) (PublicationSourceCleanupRunOutcome, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	failed, err := runner.repository.FailPublicationSourceCleanupExplicitRetryDelivery(cleanupCtx, guard, publicationSourceCleanupSafeError)
	if err != nil {
		return PublicationSourceCleanupBlocked, errors.Join(cause, fmt.Errorf("record explicit publication source cleanup retry failure: %w", err))
	}
	if !failed {
		return PublicationSourceCleanupBlocked, cause
	}
	return PublicationSourceCleanupRecoveryRequired, cause
}
