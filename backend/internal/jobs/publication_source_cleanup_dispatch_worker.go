package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

const PublicationSourceCleanupDispatchJobKind = "publication_source_cleanup_dispatch_v1"

type PublicationSourceCleanupDispatchArgs struct{}

func (PublicationSourceCleanupDispatchArgs) Kind() string {
	return PublicationSourceCleanupDispatchJobKind
}

type publicationSourceCleanupRequestDispatcher interface {
	DispatchPendingPublicationSourceCleanupRequests(context.Context) error
}

type PublicationSourceCleanupDispatchWorker struct {
	river.WorkerDefaults[PublicationSourceCleanupDispatchArgs]
	dispatcher publicationSourceCleanupRequestDispatcher
}

func NewPublicationSourceCleanupDispatchWorker(dispatcher publicationSourceCleanupRequestDispatcher) *PublicationSourceCleanupDispatchWorker {
	return &PublicationSourceCleanupDispatchWorker{dispatcher: dispatcher}
}

func (worker *PublicationSourceCleanupDispatchWorker) Work(ctx context.Context, _ *river.Job[PublicationSourceCleanupDispatchArgs]) error {
	return worker.dispatcher.DispatchPendingPublicationSourceCleanupRequests(ctx)
}

func NewPublicationSourceCleanupDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(infrastructurePeriodicInterval),
		func() (river.JobArgs, *river.InsertOpts) { return PublicationSourceCleanupDispatchArgs{}, nil },
		&river.PeriodicJobOpts{ID: "publication-source-cleanup-dispatch", RunOnStart: true},
	)
}
