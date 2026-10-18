package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationDeliveryExecutor owns the complete guarded execution lifecycle.
type PublicationDeliveryExecutor interface {
	ExecutePublicationDelivery(context.Context, uuid.UUID, int64, int) error
}

// PublicationWorker dispatches the source-free River identity to the service
// executor; it must not perform settlement or retries independently.
type PublicationWorker struct {
	river.WorkerDefaults[publication.TrackArgs]
	executor PublicationDeliveryExecutor
}

func NewPublicationWorker(executor PublicationDeliveryExecutor) *PublicationWorker {
	return &PublicationWorker{executor: executor}
}

func (worker *PublicationWorker) Work(ctx context.Context, job *river.Job[publication.TrackArgs]) error {
	if worker == nil || worker.executor == nil || job == nil || job.JobRow == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 || job.Attempt < 1 {
		return errors.New("publication worker or delivery identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := worker.executor.ExecutePublicationDelivery(ctx, job.Args.OperationID, job.ID, job.Attempt); err != nil {
		return fmt.Errorf("execute publication delivery: %w", err)
	}
	return nil
}
