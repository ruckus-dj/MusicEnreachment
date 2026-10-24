package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type obsoletePublicationOutputCleanupRunner interface {
	Execute(context.Context, uuid.UUID, int64) error
}

// ObsoletePublicationOutputCleanupWorker delegates the durable operation ID to service.
type ObsoletePublicationOutputCleanupWorker struct {
	river.WorkerDefaults[persistence.ObsoletePublicationOutputCleanupJobArgs]
	runner obsoletePublicationOutputCleanupRunner
}

func NewObsoletePublicationOutputCleanupWorker(runner obsoletePublicationOutputCleanupRunner) *ObsoletePublicationOutputCleanupWorker {
	return &ObsoletePublicationOutputCleanupWorker{runner: runner}
}

func (worker *ObsoletePublicationOutputCleanupWorker) Work(ctx context.Context, job *river.Job[persistence.ObsoletePublicationOutputCleanupJobArgs]) error {
	if worker == nil || worker.runner == nil || job == nil || job.JobRow == nil || job.Args.OperationID == uuid.Nil || job.ID < 1 {
		return errors.New("obsolete publication output cleanup worker identity is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := worker.runner.Execute(ctx, job.Args.OperationID, job.ID); err != nil {
		return fmt.Errorf("execute obsolete publication output cleanup: %w", err)
	}
	return nil
}
