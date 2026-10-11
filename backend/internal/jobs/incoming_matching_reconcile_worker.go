package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type incomingMatchingReconcileWorkerRepository interface {
	ClaimIncomingMatchingReconciliationDeliveryWithOutcome(context.Context, uuid.UUID, int64, int) (int64, persistence.IncomingMatchingReconciliationClaimOutcome, error)
	ReleaseIncomingMatchingReconciliationClaim(context.Context, uuid.UUID, int64, int, int64) (bool, error)
	MarkIncomingMatchingReconciliationComplete(context.Context, uuid.UUID, int64, int, int64) (bool, error)
}

type incomingMatchingReconcileConsumer interface {
	Reconcile(context.Context, service.IncomingMatchingReconcileDelivery) error
}

type IncomingMatchingReconcileWorker struct {
	river.WorkerDefaults[persistence.IncomingMatchingReconcileJobArgs]
	repository incomingMatchingReconcileWorkerRepository
	consumer   incomingMatchingReconcileConsumer
}

func NewIncomingMatchingReconcileWorker(repository incomingMatchingReconcileWorkerRepository, consumer incomingMatchingReconcileConsumer) *IncomingMatchingReconcileWorker {
	return &IncomingMatchingReconcileWorker{repository: repository, consumer: consumer}
}

func (worker *IncomingMatchingReconcileWorker) Work(ctx context.Context, job *river.Job[persistence.IncomingMatchingReconcileJobArgs]) error {
	if worker == nil || worker.repository == nil || worker.consumer == nil || job == nil || job.Args.EventID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return fmt.Errorf("incoming matching reconciliation worker or job is incomplete")
	}
	epoch, outcome, err := worker.repository.ClaimIncomingMatchingReconciliationDeliveryWithOutcome(ctx, job.Args.EventID, job.ID, job.Attempt)
	if err != nil {
		return err
	}
	switch outcome {
	case persistence.IncomingMatchingReconciliationNotClaimed:
		return nil
	case persistence.IncomingMatchingReconciliationBusy:
		return river.JobSnooze(time.Second)
	case persistence.IncomingMatchingReconciliationClaimed:
	default:
		return fmt.Errorf("incoming matching reconciliation claim outcome is invalid")
	}
	delivery := service.IncomingMatchingReconcileDelivery{EventID: job.Args.EventID, RiverJobID: job.ID, RiverAttempt: job.Attempt, Epoch: epoch}
	if err := worker.consumer.Reconcile(ctx, delivery); err != nil {
		if errors.Is(err, service.ErrIncomingMatchingReconciliationDeferred) {
			released, releaseErr := worker.repository.ReleaseIncomingMatchingReconciliationClaim(context.WithoutCancel(ctx), delivery.EventID, delivery.RiverJobID, delivery.RiverAttempt, delivery.Epoch)
			if releaseErr != nil {
				return fmt.Errorf("release deferred incoming matching reconciliation claim: %w", releaseErr)
			}
			if !released {
				return nil
			}
			return river.JobSnooze(time.Minute)
		}
		return errors.Join(err, worker.release(ctx, delivery))
	}
	completed, err := worker.repository.MarkIncomingMatchingReconciliationComplete(ctx, delivery.EventID, delivery.RiverJobID, delivery.RiverAttempt, delivery.Epoch)
	if err != nil {
		return errors.Join(fmt.Errorf("complete incoming matching reconciliation: %w", err), worker.release(ctx, delivery))
	}
	if !completed {
		return nil // A takeover made this delivery stale; it owns no completion.
	}
	return nil
}

func (worker *IncomingMatchingReconcileWorker) release(ctx context.Context, delivery service.IncomingMatchingReconcileDelivery) error {
	_, err := worker.repository.ReleaseIncomingMatchingReconciliationClaim(context.WithoutCancel(ctx), delivery.EventID, delivery.RiverJobID, delivery.RiverAttempt, delivery.Epoch)
	if err != nil {
		return fmt.Errorf("release incoming matching reconciliation claim: %w", err)
	}
	return nil
}

var _ river.Worker[persistence.IncomingMatchingReconcileJobArgs] = (*IncomingMatchingReconcileWorker)(nil)
