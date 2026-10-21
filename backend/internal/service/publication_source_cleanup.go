package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationSourceCleanupRepository interface {
	LoadPublicationSourceCleanupDelivery(context.Context, uuid.UUID) (persistence.PublicationSourceCleanupDeliveryInput, error)
	ClaimPublicationSourceCleanupDelivery(context.Context, persistence.PublicationSourceCleanupDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error)
	StartPublicationSourceCleanupExecution(context.Context, persistence.PublicationSourceCleanupDeliveryGuard) (bool, error)
	CancelUnstartedPublicationSourceCleanupDelivery(context.Context, persistence.PublicationSourceCleanupDeliveryGuard, string) (bool, error)
	RemovePublicationSourceCleanupItem(context.Context, persistence.PublicationSourceCleanupDeliveryGuard, uuid.UUID, func(context.Context, persistence.PublicationSourceCleanupRemovalRequest) error) (persistence.PublicationSourceCleanupRemovalOutcome, error)
	SettlePublicationSourceCleanup(context.Context, persistence.PublicationSourceCleanupDeliveryGuard) (bool, error)
	FailPublicationSourceCleanupDelivery(context.Context, persistence.PublicationSourceCleanupDeliveryGuard, string) (bool, error)
}

type publicationSourceCleanupRemover interface {
	Remove(context.Context, string, string, int64, time.Time) error
}

type PublicationSourceCleanupRunOutcome string

const (
	PublicationSourceCleanupBlocked          PublicationSourceCleanupRunOutcome = "blocked"
	PublicationSourceCleanupCancelled        PublicationSourceCleanupRunOutcome = "cancelled"
	PublicationSourceCleanupCompleted        PublicationSourceCleanupRunOutcome = "completed"
	PublicationSourceCleanupRecoveryRequired PublicationSourceCleanupRunOutcome = "recovery_required"
)

const publicationSourceCleanupSafeError = "One or more publication source files could not be safely removed."

// PublicationSourceCleanupRunner performs one exact River delivery. It is
// deliberately dormant unless explicitly invoked by a caller.
type PublicationSourceCleanupRunner struct {
	repository publicationSourceCleanupRepository
	remover    publicationSourceCleanupRemover
	bootUUID   uuid.UUID
}

func NewPublicationSourceCleanupRunner(repository publicationSourceCleanupRepository, remover publicationSourceCleanupRemover, bootUUID uuid.UUID) (*PublicationSourceCleanupRunner, error) {
	if repository == nil || remover == nil || bootUUID == uuid.Nil {
		return nil, fmt.Errorf("publication source cleanup runner requires repository, remover, and boot identity")
	}
	return &PublicationSourceCleanupRunner{repository: repository, remover: remover, bootUUID: bootUUID}, nil
}

func (runner *PublicationSourceCleanupRunner) ExecutePublicationSourceCleanupDelivery(ctx context.Context, operationID uuid.UUID, riverJobID int64, riverAttempt int) (PublicationSourceCleanupRunOutcome, error) {
	if ctx == nil || operationID == uuid.Nil || riverJobID <= 0 || riverAttempt <= 0 {
		return PublicationSourceCleanupBlocked, fmt.Errorf("execute publication source cleanup: valid context and River delivery identity are required")
	}
	input, err := runner.repository.LoadPublicationSourceCleanupDelivery(ctx, operationID)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("load publication source cleanup delivery: %w", err)
	}
	if input.OperationID != operationID || input.ApplicationAttempt <= 0 || input.Epoch == uuid.Nil || input.PositionID == uuid.Nil || len(input.ItemIDs) == 0 {
		return PublicationSourceCleanupBlocked, fmt.Errorf("load publication source cleanup delivery: invalid delivery input")
	}
	claimID, err := uuid.NewRandom()
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("create publication source cleanup claim identity: %w", err)
	}
	tokenID, err := uuid.NewRandom()
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("create publication source cleanup delivery token: %w", err)
	}
	guard := persistence.PublicationSourceCleanupDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: input.ApplicationAttempt, RiverJobID: riverJobID,
		RiverAttempt: riverAttempt, ClaimUUID: claimID, BootUUID: runner.bootUUID,
		DeliveryToken: tokenID.String(), Epoch: input.Epoch, PositionID: input.PositionID,
	}
	if !input.RemovalEnabled {
		return runner.cancelIfUnstarted(ctx, guard)
	}
	claim, err := runner.repository.ClaimPublicationSourceCleanupDelivery(ctx, guard)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("claim publication source cleanup delivery: %w", err)
	}
	if claim != persistence.PublicationDeliveryAcquired {
		return PublicationSourceCleanupBlocked, nil
	}
	started, err := runner.repository.StartPublicationSourceCleanupExecution(ctx, guard)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("start publication source cleanup execution: %w", err)
	}
	if !started {
		return runner.cancelIfUnstarted(ctx, guard)
	}
	for _, itemID := range input.ItemIDs {
		result, removeErr := runner.repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(ctx context.Context, request persistence.PublicationSourceCleanupRemovalRequest) error {
			return runner.remover.Remove(ctx, request.ConfiguredRootPath, request.RelativePath, request.SizeBytes, request.MTime)
		})
		if removeErr != nil || result == persistence.PublicationSourceCleanupRemovalBlocked {
			failed, failErr := runner.repository.FailPublicationSourceCleanupDelivery(ctx, guard, publicationSourceCleanupSafeError)
			if failErr != nil {
				return PublicationSourceCleanupBlocked, errors.Join(wrapCleanupError("record publication source cleanup failure", failErr), wrapCleanupError("remove publication source cleanup item", removeErr))
			}
			if !failed {
				return PublicationSourceCleanupBlocked, wrapCleanupError("remove publication source cleanup item", removeErr)
			}
			return PublicationSourceCleanupRecoveryRequired, wrapCleanupError("remove publication source cleanup item", removeErr)
		}
		if result != persistence.PublicationSourceCleanupRemovalCompleted && result != persistence.PublicationSourceCleanupRemovalAlreadyCompleted {
			return PublicationSourceCleanupBlocked, fmt.Errorf("remove publication source cleanup item: unknown result %q", result)
		}
	}
	settled, err := runner.repository.SettlePublicationSourceCleanup(ctx, guard)
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("settle publication source cleanup delivery: %w", err)
	}
	if !settled {
		return PublicationSourceCleanupBlocked, nil
	}
	return PublicationSourceCleanupCompleted, nil
}

func (runner *PublicationSourceCleanupRunner) cancelIfUnstarted(ctx context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard) (PublicationSourceCleanupRunOutcome, error) {
	cancelled, err := runner.repository.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "Publication source removal is disabled.")
	if err != nil {
		return PublicationSourceCleanupBlocked, fmt.Errorf("cancel unstarted publication source cleanup delivery: %w", err)
	}
	if cancelled {
		return PublicationSourceCleanupCancelled, nil
	}
	return PublicationSourceCleanupBlocked, nil
}

func wrapCleanupError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
