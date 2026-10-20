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

type publicationReconciliationDeliveryRepository interface {
	ClaimPublicationReconciliationDelivery(context.Context, uuid.UUID, int64, int) (persistence.PublicationReconciliationDeliveryGuard, persistence.PublicationReconciliationClaimResult, error)
	ReleasePublicationReconciliationDelivery(context.Context, persistence.PublicationReconciliationDeliveryGuard) (bool, error)
}

type publicationReconciler interface {
	ReconcilePublication(context.Context, persistence.PublicationReconciliationDeliveryGuard) error
}

// PublicationReconciliationWorker owns cleanup of a claimed delivery whenever
// reconciliation returns an error. Successful reconciliation terminalizes the
// claim itself, in the park or automatic-admission transaction.
type PublicationReconciliationWorker struct {
	river.WorkerDefaults[persistence.PublicationReconciliationJobArgs]
	repository publicationReconciliationDeliveryRepository
	reconciler publicationReconciler
	operations *service.Operations
}

func NewPublicationReconciliationWorker(repository publicationReconciliationDeliveryRepository, reconciler publicationReconciler, operations *service.Operations) *PublicationReconciliationWorker {
	return &PublicationReconciliationWorker{repository: repository, reconciler: reconciler, operations: operations}
}

func (worker *PublicationReconciliationWorker) Work(ctx context.Context, job *river.Job[persistence.PublicationReconciliationJobArgs]) error {
	if worker == nil || worker.repository == nil || worker.reconciler == nil || job == nil || job.JobRow == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return errors.New("publication reconciliation worker or delivery identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	guard, result, err := worker.repository.ClaimPublicationReconciliationDelivery(ctx, job.Args.OperationID, job.ID, job.Attempt)
	if err != nil {
		return fmt.Errorf("claim publication reconciliation delivery: %w", err)
	}
	switch result {
	case persistence.PublicationReconciliationStale:
		return nil
	case persistence.PublicationReconciliationBusy:
		return errors.New("publication reconciliation delivery is already claimed")
	case persistence.PublicationReconciliationClaimed:
	default:
		return errors.New("publication reconciliation delivery was not claimed")
	}
	if err := worker.reconciler.ReconcilePublication(ctx, guard); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		released, releaseErr := worker.repository.ReleasePublicationReconciliationDelivery(cleanupCtx, guard)
		if releaseErr != nil {
			return fmt.Errorf("release failed publication reconciliation delivery: %v: %w", releaseErr, err)
		}
		if !released {
			return nil
		}
		if worker.operations != nil {
			worker.operations.Notify(job.Args.OperationID)
		}
		return fmt.Errorf("reconcile publication delivery: %w", err)
	}
	if worker.operations != nil {
		worker.operations.Notify(job.Args.OperationID)
	}
	return nil
}

var _ river.Worker[persistence.PublicationReconciliationJobArgs] = (*PublicationReconciliationWorker)(nil)
