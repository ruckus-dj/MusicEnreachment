package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "operation_cleanup_v1" }

type operationCleaner interface {
	DeleteSucceededBefore(context.Context, time.Time) error
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	repository operationCleaner
}

func NewCleanupWorker(repository operationCleaner) *CleanupWorker {
	return &CleanupWorker{repository: repository}
}

func (worker *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	return worker.repository.DeleteSucceededBefore(ctx, time.Now().UTC().Add(-24*time.Hour))
}

func NewCleanupPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
		&river.PeriodicJobOpts{ID: "setup-operation-cleanup", RunOnStart: true},
	)
}
