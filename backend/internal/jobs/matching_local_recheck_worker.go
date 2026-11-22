package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const MatchingLocalRecheckJobKind = "matching_local_recheck_v1"
const MatchingLocalRecheckQueue = "matching-local-recheck"

// matchingLocalRecheckInterval bounds how long a due Local recheck waits for the
// next dispatch. Each tick processes only a bounded batch, and active-job
// uniqueness below prevents overlapping ticks.
const matchingLocalRecheckInterval = time.Minute

type MatchingLocalRecheckArgs struct{}

func (MatchingLocalRecheckArgs) Kind() string {
	return MatchingLocalRecheckJobKind
}

type matchingLocalRecheckRunner interface {
	RunDueMatchingLocalRechecks(context.Context) error
}

type MatchingLocalRecheckWorker struct {
	river.WorkerDefaults[MatchingLocalRecheckArgs]
	runner matchingLocalRecheckRunner
}

func NewMatchingLocalRecheckWorker(runner matchingLocalRecheckRunner) *MatchingLocalRecheckWorker {
	return &MatchingLocalRecheckWorker{runner: runner}
}

// A complete, rate-limited artist browse can exceed River's default
// deadline. Use a dedicated queue so it cannot occupy the matching worker.
func (*MatchingLocalRecheckWorker) Timeout(*river.Job[MatchingLocalRecheckArgs]) time.Duration {
	return -1
}

func (worker *MatchingLocalRecheckWorker) Work(ctx context.Context, job *river.Job[MatchingLocalRecheckArgs]) error {
	if worker == nil || worker.runner == nil || job == nil {
		return fmt.Errorf("local matching recheck worker is unavailable")
	}
	return worker.runner.RunDueMatchingLocalRechecks(ctx)
}

var _ river.Worker[MatchingLocalRecheckArgs] = (*MatchingLocalRecheckWorker)(nil)

// matchingLocalRecheckInsert keeps one due-recheck tick active at a time, so a
// slow provider call adds no duplicate work while the interval still makes newly
// due obligations visible promptly.
func matchingLocalRecheckInsert() (river.JobArgs, *river.InsertOpts) {
	return MatchingLocalRecheckArgs{}, &river.InsertOpts{Queue: MatchingLocalRecheckQueue, MaxAttempts: 1, UniqueOpts: river.UniqueOpts{
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

func NewMatchingLocalRecheckPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(matchingLocalRecheckInterval),
		matchingLocalRecheckInsert,
		&river.PeriodicJobOpts{ID: "matching-local-recheck", RunOnStart: true},
	)
}
