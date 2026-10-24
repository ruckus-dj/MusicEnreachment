package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type obsoleteCleanupRunnerSpy struct {
	id    uuid.UUID
	jobID int64
	err   error
	calls int
}

func (s *obsoleteCleanupRunnerSpy) Execute(_ context.Context, id uuid.UUID, jobID int64) error {
	s.calls++
	s.id, s.jobID = id, jobID
	return s.err
}

func TestObsoletePublicationOutputCleanupWorkerForwardsOnlyDurableIdentity(t *testing.T) {
	id := uuid.New()
	runner := new(obsoleteCleanupRunnerSpy)
	worker := NewObsoletePublicationOutputCleanupWorker(runner)
	job := &river.Job[persistence.ObsoletePublicationOutputCleanupJobArgs]{JobRow: &rivertype.JobRow{ID: 19}, Args: persistence.ObsoletePublicationOutputCleanupJobArgs{OperationID: id}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || runner.id != id || runner.jobID != 19 {
		t.Fatalf("runner call = %+v", runner)
	}
}

func TestObsoletePublicationOutputCleanupWorkerRejectsIncompleteIdentity(t *testing.T) {
	id := uuid.New()
	runner := new(obsoleteCleanupRunnerSpy)
	worker := NewObsoletePublicationOutputCleanupWorker(runner)
	valid := &river.Job[persistence.ObsoletePublicationOutputCleanupJobArgs]{JobRow: &rivertype.JobRow{ID: 19}, Args: persistence.ObsoletePublicationOutputCleanupJobArgs{OperationID: id}}
	cases := []*river.Job[persistence.ObsoletePublicationOutputCleanupJobArgs]{nil, {}, {JobRow: &rivertype.JobRow{ID: 19}}, {JobRow: &rivertype.JobRow{ID: 0}, Args: valid.Args}}
	for i, job := range cases {
		if err := worker.Work(context.Background(), job); err == nil {
			t.Errorf("case %d: expected identity error", i)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("runner called %d times", runner.calls)
	}
}

func TestObsoletePublicationOutputCleanupWorkerPropagatesRunnerFailure(t *testing.T) {
	want := errors.New("database unavailable")
	runner := &obsoleteCleanupRunnerSpy{err: want}
	worker := NewObsoletePublicationOutputCleanupWorker(runner)
	job := &river.Job[persistence.ObsoletePublicationOutputCleanupJobArgs]{JobRow: &rivertype.JobRow{ID: 2}, Args: persistence.ObsoletePublicationOutputCleanupJobArgs{OperationID: uuid.New()}}
	if err := worker.Work(context.Background(), job); !errors.Is(err, want) {
		t.Fatalf("Work error = %v", err)
	}
}
