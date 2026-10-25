package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const PublicationReconciliationDispatchJobKind = "publication_reconciliation_dispatch_v1"

// publicationReconciliationDispatchInterval keeps durable pending publication
// reconciliations dispatched promptly. A reconciliation stays pending until this
// dispatch inserts its job, so the hourly housekeeping cadence leaves freshly
// waiting work undispatched for most of an hour. Active-job uniqueness below
// keeps the minute cadence from piling up duplicate dispatches.
const publicationReconciliationDispatchInterval = time.Minute

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

// publicationReconciliationDispatchInsert builds the periodic dispatch job and
// makes it unique while an earlier dispatch is still available, pending,
// running, retryable or scheduled, so the interval adds responsiveness without
// adding concurrent dispatchers of the same pending work.
func publicationReconciliationDispatchInsert() (river.JobArgs, *river.InsertOpts) {
	return PublicationReconciliationDispatchArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs: true,
		ByState: []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRunning,
			rivertype.JobStateRetryable,
			rivertype.JobStateScheduled,
		},
	}}
}

func NewPublicationReconciliationDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(publicationReconciliationDispatchInterval),
		publicationReconciliationDispatchInsert,
		&river.PeriodicJobOpts{ID: "publication-reconciliation-dispatch", RunOnStart: true},
	)
}
