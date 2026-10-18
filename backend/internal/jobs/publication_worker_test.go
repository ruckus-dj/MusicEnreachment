package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationExecutorFunc func(context.Context, uuid.UUID, int64, int) error

func (executor publicationExecutorFunc) ExecutePublicationDelivery(ctx context.Context, id uuid.UUID, jobID int64, attempt int) error {
	return executor(ctx, id, jobID, attempt)
}

func TestPublicationWorkerDispatchesIdentity(t *testing.T) {
	opID := uuid.New()
	called := false
	worker := NewPublicationWorker(publicationExecutorFunc(func(_ context.Context, gotID uuid.UUID, jobID int64, attempt int) error {
		called = true
		if gotID != opID || jobID != 44 || attempt != 2 {
			t.Fatalf("unexpected delivery identity: %s %d %d", gotID, jobID, attempt)
		}
		return nil
	}))
	if err := worker.Work(context.Background(), publicationWorkerTestJob(opID, 44, 2)); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("executor was not called")
	}
}

func TestPublicationWorkerReturnsExecutorError(t *testing.T) {
	want := errors.New("executor failed")
	worker := NewPublicationWorker(publicationExecutorFunc(func(context.Context, uuid.UUID, int64, int) error { return want }))
	err := worker.Work(context.Background(), publicationWorkerTestJob(uuid.New(), 44, 2))
	if !errors.Is(err, want) {
		t.Fatalf("Work() error = %v, want %v", err, want)
	}
}

func TestPublicationWorkerHonorsCancellation(t *testing.T) {
	called := false
	worker := NewPublicationWorker(publicationExecutorFunc(func(context.Context, uuid.UUID, int64, int) error { called = true; return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := worker.Work(ctx, publicationWorkerTestJob(uuid.New(), 44, 2))
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("Work() = %v, called = %v", err, called)
	}
}

func TestPublicationWorkerRejectsIncompleteIdentity(t *testing.T) {
	opID := uuid.New()
	newJob := func() *river.Job[publication.TrackArgs] {
		return publicationWorkerTestJob(opID, 44, 2)
	}
	var nilWorker *PublicationWorker
	for _, test := range []struct {
		name        string
		job         *river.Job[publication.TrackArgs]
		nilWorker   bool
		nilExecutor bool
	}{
		{name: "nil receiver", job: newJob(), nilWorker: true},
		{name: "nil executor", job: newJob(), nilExecutor: true},
		{name: "nil job"},
		{name: "nil job row", job: &river.Job[publication.TrackArgs]{Args: publication.TrackArgs{OperationID: opID}}},
		{name: "invalid job ID", job: publicationWorkerTestJob(opID, 0, 2)},
		{name: "invalid attempt", job: publicationWorkerTestJob(opID, 44, 0)},
		{name: "nil operation ID", job: publicationWorkerTestJob(uuid.Nil, 44, 2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			worker := NewPublicationWorker(publicationExecutorFunc(func(context.Context, uuid.UUID, int64, int) error {
				calls++
				return nil
			}))
			if test.nilWorker {
				worker = nilWorker
			} else if test.nilExecutor {
				worker = NewPublicationWorker(nil)
			}
			var err error
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("Work panicked: %v", recovered)
					}
				}()
				err = worker.Work(context.Background(), test.job)
			}()
			if err == nil {
				t.Fatal("Work() error = nil, want incomplete identity error")
			}
			if calls != 0 {
				t.Fatalf("executor calls = %d, want 0", calls)
			}
		})
	}
}

func publicationWorkerTestJob(operationID uuid.UUID, jobID int64, attempt int) *river.Job[publication.TrackArgs] {
	return &river.Job[publication.TrackArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   publication.TrackArgs{OperationID: operationID},
	}
}
