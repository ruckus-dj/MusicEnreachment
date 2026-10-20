package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

const PublicationReconciliationDispatchJobKind = "publication_reconciliation_dispatch_v1"

type PublicationReconciliationDispatchArgs struct{}

func (PublicationReconciliationDispatchArgs) Kind() string {
	return PublicationReconciliationDispatchJobKind
}

type publicationReconciliationDispatcher interface {
	DispatchPendingPublicationReconciliations(context.Context) error
}

type PublicationReconciliationDispatchWorker struct {
	river.WorkerDefaults[PublicationReconciliationDispatchArgs]
	dispatcher publicationReconciliationDispatcher
}

func NewPublicationReconciliationDispatchWorker(dispatcher publicationReconciliationDispatcher) *PublicationReconciliationDispatchWorker {
	return &PublicationReconciliationDispatchWorker{dispatcher: dispatcher}
}

func (worker *PublicationReconciliationDispatchWorker) Work(ctx context.Context, _ *river.Job[PublicationReconciliationDispatchArgs]) error {
	return worker.dispatcher.DispatchPendingPublicationReconciliations(ctx)
}

func NewPublicationReconciliationDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(infrastructurePeriodicInterval),
		func() (river.JobArgs, *river.InsertOpts) { return PublicationReconciliationDispatchArgs{}, nil },
		&river.PeriodicJobOpts{ID: "publication-reconciliation-dispatch", RunOnStart: true},
	)
}
