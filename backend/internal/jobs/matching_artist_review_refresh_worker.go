package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const ArtistMatchingReviewRefreshJobKind = "matching_artist_review_refresh_v1"

// artistMatchingReviewRefreshInterval bounds how long a saved artist review can
// lag behind an accepted provider cache refresh. The detector itself scans only
// a bounded number of keyset pages per tick, so a short interval does not turn
// into unbounded work.
const artistMatchingReviewRefreshInterval = 30 * time.Second

type ArtistMatchingReviewRefreshArgs struct{}

func (ArtistMatchingReviewRefreshArgs) Kind() string {
	return ArtistMatchingReviewRefreshJobKind
}

type artistMatchingReviewRefreshDetector interface {
	Refresh(context.Context) error
}

type ArtistMatchingReviewRefreshWorker struct {
	river.WorkerDefaults[ArtistMatchingReviewRefreshArgs]
	detector artistMatchingReviewRefreshDetector
}

func NewArtistMatchingReviewRefreshWorker(detector artistMatchingReviewRefreshDetector) *ArtistMatchingReviewRefreshWorker {
	return &ArtistMatchingReviewRefreshWorker{detector: detector}
}

func (worker *ArtistMatchingReviewRefreshWorker) Work(ctx context.Context, job *river.Job[ArtistMatchingReviewRefreshArgs]) error {
	if worker == nil || worker.detector == nil || job == nil {
		return fmt.Errorf("artist matching review refresh worker is unavailable")
	}
	return worker.detector.Refresh(ctx)
}

var _ river.Worker[ArtistMatchingReviewRefreshArgs] = (*ArtistMatchingReviewRefreshWorker)(nil)

// artistMatchingReviewRefreshInsert keeps one detector tick active at a time,
// so a slow tick adds no duplicate work while the interval still makes newly
// stale reviews visible promptly.
func artistMatchingReviewRefreshInsert() (river.JobArgs, *river.InsertOpts) {
	return ArtistMatchingReviewRefreshArgs{}, &river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{
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

func NewArtistMatchingReviewRefreshPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(artistMatchingReviewRefreshInterval),
		artistMatchingReviewRefreshInsert,
		&river.PeriodicJobOpts{ID: "matching-artist-review-refresh", RunOnStart: true},
	)
}
