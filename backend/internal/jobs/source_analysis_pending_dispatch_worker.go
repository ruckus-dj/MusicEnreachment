package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const SourceAnalysisPendingDispatchJobKind = "source_analysis_pending_dispatch_v1"

type SourceAnalysisPendingDispatchArgs struct{}

func (SourceAnalysisPendingDispatchArgs) Kind() string { return SourceAnalysisPendingDispatchJobKind }

type sourceAnalysisPendingRootDispatcher interface {
	DispatchPending(context.Context) error
}

type SourceAnalysisPendingDispatchWorker struct {
	river.WorkerDefaults[SourceAnalysisPendingDispatchArgs]
	dispatcher sourceAnalysisPendingRootDispatcher
}

func NewSourceAnalysisPendingDispatchWorker(dispatcher sourceAnalysisPendingRootDispatcher) *SourceAnalysisPendingDispatchWorker {
	return &SourceAnalysisPendingDispatchWorker{dispatcher: dispatcher}
}

func (worker *SourceAnalysisPendingDispatchWorker) Work(ctx context.Context, _ *river.Job[SourceAnalysisPendingDispatchArgs]) error {
	return worker.dispatcher.DispatchPending(ctx)
}

func NewSourceAnalysisPendingDispatchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return SourceAnalysisPendingDispatchArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{
				ByArgs: true,
				ByState: []rivertype.JobState{
					rivertype.JobStateAvailable,
					rivertype.JobStatePending,
					rivertype.JobStateRunning,
					rivertype.JobStateRetryable,
					rivertype.JobStateScheduled,
				},
			}}
		},
		&river.PeriodicJobOpts{ID: "source-analysis-pending-dispatch", RunOnStart: true},
	)
}
