package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryDeletionDeliveryExecutor interface {
	ExecuteDelivery(context.Context, uuid.UUID, int64, int) error
	DeliveryFailureWasRecorded(context.Context, uuid.UUID, int64) (bool, error)
}

type operationNotifier interface {
	Notify(uuid.UUID)
}

// LibraryDeletionWorker dispatches only the durable operation identity. The
// service owns delivery fencing, filesystem mutation, and recovery authority.
type LibraryDeletionWorker struct {
	river.WorkerDefaults[persistence.LibraryDeletionOperationArgs]
	executor   libraryDeletionDeliveryExecutor
	operations operationNotifier
}

func NewLibraryDeletionWorker(executor libraryDeletionDeliveryExecutor, operations operationNotifier) *LibraryDeletionWorker {
	return &LibraryDeletionWorker{executor: executor, operations: operations}
}

func (worker *LibraryDeletionWorker) Work(ctx context.Context, job *river.Job[persistence.LibraryDeletionOperationArgs]) error {
	if worker == nil || worker.executor == nil || ctx == nil || job == nil || job.JobRow == nil ||
		job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return errors.New("library deletion worker or delivery identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	executeErr := worker.executor.ExecuteDelivery(ctx, job.Args.OperationID, job.ID, job.Attempt)
	if worker.operations != nil {
		worker.operations.Notify(job.Args.OperationID)
	}
	if executeErr == nil {
		return nil
	}
	handled, lookupErr := worker.executor.DeliveryFailureWasRecorded(ctx, job.Args.OperationID, job.ID)
	if lookupErr != nil {
		return fmt.Errorf("read library deletion failure outcome: %w", errors.Join(executeErr, lookupErr))
	}
	if handled {
		slog.WarnContext(ctx, "library deletion failed durably and requires explicit retry", "operation_id", job.Args.OperationID, "error", executeErr)
		return nil
	}
	return fmt.Errorf("execute library deletion delivery: %w", executeErr)
}

var _ river.Worker[persistence.LibraryDeletionOperationArgs] = (*LibraryDeletionWorker)(nil)

var _ libraryDeletionDeliveryExecutor = (*service.LibraryDeletionService)(nil)
