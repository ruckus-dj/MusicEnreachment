package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type reconcileWorkerRepositoryFake struct {
	epoch      int64
	outcome    persistence.IncomingMatchingReconciliationClaimOutcome
	claimedID  uuid.UUID
	claimedJob int64
	claimedTry int
	completed  []any
	released   []any
	completeOK bool
	releaseOK  bool
}

func (fake *reconcileWorkerRepositoryFake) ClaimIncomingMatchingReconciliationDeliveryWithOutcome(_ context.Context, eventID uuid.UUID, jobID int64, attempt int) (int64, persistence.IncomingMatchingReconciliationClaimOutcome, error) {
	fake.claimedID, fake.claimedJob, fake.claimedTry = eventID, jobID, attempt
	return fake.epoch, fake.outcome, nil
}

func (fake *reconcileWorkerRepositoryFake) ReleaseIncomingMatchingReconciliationClaim(_ context.Context, eventID uuid.UUID, jobID int64, attempt int, epoch int64) (bool, error) {
	fake.released = []any{eventID, jobID, attempt, epoch}
	return fake.releaseOK, nil
}

func (fake *reconcileWorkerRepositoryFake) MarkIncomingMatchingReconciliationComplete(_ context.Context, eventID uuid.UUID, jobID int64, attempt int, epoch int64) (bool, error) {
	fake.completed = []any{eventID, jobID, attempt, epoch}
	return fake.completeOK, nil
}

type reconcileWorkerConsumerFake struct {
	err        error
	deliveries []service.IncomingMatchingReconcileDelivery
}

func (fake *reconcileWorkerConsumerFake) Reconcile(_ context.Context, delivery service.IncomingMatchingReconcileDelivery) error {
	fake.deliveries = append(fake.deliveries, delivery)
	return fake.err
}

func reconcileWorkerTestJob(id uuid.UUID, jobID int64, attempt int) *river.Job[persistence.IncomingMatchingReconcileJobArgs] {
	return &river.Job[persistence.IncomingMatchingReconcileJobArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   persistence.IncomingMatchingReconcileJobArgs{EventID: id},
	}
}

func TestIncomingMatchingReconcileWorkerBusyAndStaleClaims(t *testing.T) {
	eventID := uuid.New()
	for _, test := range []struct {
		name    string
		outcome persistence.IncomingMatchingReconciliationClaimOutcome
		wantErr bool
	}{
		{name: "busy snoozes", outcome: persistence.IncomingMatchingReconciliationBusy, wantErr: true},
		{name: "stale no-op", outcome: persistence.IncomingMatchingReconciliationNotClaimed},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &reconcileWorkerRepositoryFake{outcome: test.outcome}
			consumer := &reconcileWorkerConsumerFake{}
			err := NewIncomingMatchingReconcileWorker(repository, consumer).Work(context.Background(), reconcileWorkerTestJob(eventID, 19, 2))
			if (err != nil) != test.wantErr {
				t.Fatalf("Work error = %v, want error %t", err, test.wantErr)
			}
			if len(consumer.deliveries) != 0 || len(repository.completed) != 0 || len(repository.released) != 0 {
				t.Fatalf("non-claimed delivery did work: consumer=%v completed=%v released=%v", consumer.deliveries, repository.completed, repository.released)
			}
		})
	}
}

func TestIncomingMatchingReconcileWorkerDeferredErrorAndSuccessFences(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.New()
	const jobID, attempt, epoch = int64(47), 3, int64(11)
	delivery := []any{eventID, jobID, attempt, epoch}

	t.Run("deferred releases then snoozes", func(t *testing.T) {
		repository := &reconcileWorkerRepositoryFake{epoch: epoch, outcome: persistence.IncomingMatchingReconciliationClaimed, releaseOK: true}
		consumer := &reconcileWorkerConsumerFake{err: service.ErrIncomingMatchingReconciliationDeferred}
		err := NewIncomingMatchingReconcileWorker(repository, consumer).Work(ctx, reconcileWorkerTestJob(eventID, jobID, attempt))
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) || snooze.Duration != time.Minute {
			t.Fatalf("Work error = %v, want one-minute snooze", err)
		}
		if !equalReconcileWorkerFence(repository.released, delivery) || len(repository.completed) != 0 {
			t.Fatalf("deferred delivery fence mismatch: released=%v completed=%v", repository.released, repository.completed)
		}
	})

	t.Run("consumer error releases owned claim", func(t *testing.T) {
		repository := &reconcileWorkerRepositoryFake{epoch: epoch, outcome: persistence.IncomingMatchingReconciliationClaimed, releaseOK: true}
		consumer := &reconcileWorkerConsumerFake{err: errors.New("consumer failed")}
		err := NewIncomingMatchingReconcileWorker(repository, consumer).Work(ctx, reconcileWorkerTestJob(eventID, jobID, attempt))
		if err == nil || !equalReconcileWorkerFence(repository.released, delivery) || len(repository.completed) != 0 {
			t.Fatalf("Work error=%v released=%v completed=%v", err, repository.released, repository.completed)
		}
	})

	t.Run("success completes exact attempt epoch", func(t *testing.T) {
		repository := &reconcileWorkerRepositoryFake{epoch: epoch, outcome: persistence.IncomingMatchingReconciliationClaimed, completeOK: true}
		consumer := &reconcileWorkerConsumerFake{}
		if err := NewIncomingMatchingReconcileWorker(repository, consumer).Work(ctx, reconcileWorkerTestJob(eventID, jobID, attempt)); err != nil {
			t.Fatal(err)
		}
		if !equalReconcileWorkerFence(repository.completed, delivery) || len(repository.released) != 0 {
			t.Fatalf("successful delivery fence mismatch: completed=%v released=%v", repository.completed, repository.released)
		}
		if len(consumer.deliveries) != 1 || consumer.deliveries[0] != (service.IncomingMatchingReconcileDelivery{EventID: eventID, RiverJobID: jobID, RiverAttempt: attempt, Epoch: epoch}) {
			t.Fatalf("consumer deliveries = %#v", consumer.deliveries)
		}
	})
}

func equalReconcileWorkerFence(actual, expected []any) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
