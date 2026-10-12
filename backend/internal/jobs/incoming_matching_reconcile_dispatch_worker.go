package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

const IncomingMatchingReconcileDispatchJobKind = "matching_reconcile_dispatch_v1"

type IncomingMatchingReconcileDispatchArgs struct{}

func (IncomingMatchingReconcileDispatchArgs) Kind() string {
	return IncomingMatchingReconcileDispatchJobKind
}

type IncomingMatchingReconcileDispatchWorker struct {
	river.WorkerDefaults[IncomingMatchingReconcileDispatchArgs]
	dispatcher incomingMatchingReconcileDispatcher
}

func NewIncomingMatchingReconcileDispatchWorker(dispatcher incomingMatchingReconcileDispatcher) *IncomingMatchingReconcileDispatchWorker {
	return &IncomingMatchingReconcileDispatchWorker{dispatcher: dispatcher}
}

func (worker *IncomingMatchingReconcileDispatchWorker) Work(ctx context.Context, _ *river.Job[IncomingMatchingReconcileDispatchArgs]) error {
	return worker.dispatcher.DispatchPendingMatchingReconciliations(ctx)
}

func NewIncomingMatchingReconcileDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(infrastructurePeriodicInterval),
		func() (river.JobArgs, *river.InsertOpts) { return IncomingMatchingReconcileDispatchArgs{}, nil },
		&river.PeriodicJobOpts{ID: "incoming-matching-reconcile-dispatch", RunOnStart: true},
	)
}
