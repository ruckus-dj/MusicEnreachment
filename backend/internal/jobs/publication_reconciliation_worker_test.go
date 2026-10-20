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

type publicationReconciliationWorkerRepository struct {
	guard         persistence.PublicationReconciliationDeliveryGuard
	result        persistence.PublicationReconciliationClaimResult
	claimErr      error
	releaseErr    error
	claimCalls    int
	releaseCalls  int
	releaseCtx    context.Context
	releaseCtxErr error
}

func (repository *publicationReconciliationWorkerRepository) ClaimPublicationReconciliationDelivery(_ context.Context, _ uuid.UUID, _ int64, _ int) (persistence.PublicationReconciliationDeliveryGuard, persistence.PublicationReconciliationClaimResult, error) {
	repository.claimCalls++
	return repository.guard, repository.result, repository.claimErr
}

func (repository *publicationReconciliationWorkerRepository) ReleasePublicationReconciliationDelivery(ctx context.Context, _ persistence.PublicationReconciliationDeliveryGuard) (bool, error) {
	repository.releaseCalls++
	repository.releaseCtx = ctx
	repository.releaseCtxErr = ctx.Err()
	return true, repository.releaseErr
}

type publicationReconcilerFunc func(context.Context, persistence.PublicationReconciliationDeliveryGuard) error

func (reconciler publicationReconcilerFunc) ReconcilePublication(ctx context.Context, guard persistence.PublicationReconciliationDeliveryGuard) error {
	return reconciler(ctx, guard)
}

func TestPublicationReconciliationWorkerClaimOutcomes(t *testing.T) {
	for _, test := range []struct {
		name      string
		result    persistence.PublicationReconciliationClaimResult
		wantError bool
	}{
		{name: "stale stops", result: persistence.PublicationReconciliationStale},
		{name: "busy retries", result: persistence.PublicationReconciliationBusy, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &publicationReconciliationWorkerRepository{result: test.result}
			reconciled := false
			worker := NewPublicationReconciliationWorker(repository, publicationReconcilerFunc(func(context.Context, persistence.PublicationReconciliationDeliveryGuard) error {
				reconciled = true
				return nil
			}), nil)
			err := worker.Work(context.Background(), publicationReconciliationTestJob(uuid.New(), 61, 1))
			if (err != nil) != test.wantError {
				t.Fatalf("Work() error = %v, wantError=%v", err, test.wantError)
			}
			if repository.claimCalls != 1 || repository.releaseCalls != 0 || reconciled {
				t.Fatalf("claim/release/reconcile calls = %d/%d/%v", repository.claimCalls, repository.releaseCalls, reconciled)
			}
		})
	}
}

func TestPublicationReconciliationWorkerReleasesOwnedClaimOnRetryAndCancellation(t *testing.T) {
	want := errors.New("reconciliation failed")
	repository := &publicationReconciliationWorkerRepository{
		result: persistence.PublicationReconciliationClaimed,
		guard:  persistence.PublicationReconciliationDeliveryGuard{OperationID: uuid.New(), ClaimToken: uuid.New()},
	}
	var cancel context.CancelFunc
	worker := NewPublicationReconciliationWorker(repository, publicationReconcilerFunc(func(ctx context.Context, _ persistence.PublicationReconciliationDeliveryGuard) error {
		cancel()
		return want
	}), nil)
	ctx, cancelContext := context.WithCancel(context.Background())
	cancel = cancelContext
	err := worker.Work(ctx, publicationReconciliationTestJob(repository.guard.OperationID, 61, 1))
	if !errors.Is(err, want) {
		t.Fatalf("Work() error = %v, want wrapped reconciliation error", err)
	}
	if repository.releaseCalls != 1 || repository.releaseCtx == nil || repository.releaseCtxErr != nil {
		t.Fatalf("release calls/context = %d/%v", repository.releaseCalls, repository.releaseCtx)
	}
	if _, ok := repository.releaseCtx.Deadline(); !ok {
		t.Fatal("release cleanup context has no deadline")
	}
}

func TestPublicationReconciliationWorkerDoesNotReleaseSuccessfulTerminalization(t *testing.T) {
	repository := &publicationReconciliationWorkerRepository{
		result: persistence.PublicationReconciliationClaimed,
		guard:  persistence.PublicationReconciliationDeliveryGuard{OperationID: uuid.New(), ClaimToken: uuid.New()},
	}
	worker := NewPublicationReconciliationWorker(repository, publicationReconcilerFunc(func(context.Context, persistence.PublicationReconciliationDeliveryGuard) error { return nil }), nil)
	if err := worker.Work(context.Background(), publicationReconciliationTestJob(repository.guard.OperationID, 61, 1)); err != nil {
		t.Fatal(err)
	}
	if repository.releaseCalls != 0 {
		t.Fatalf("release calls = %d, want service-owned terminalization", repository.releaseCalls)
	}
}

func publicationReconciliationTestJob(operationID uuid.UUID, jobID int64, attempt int) *river.Job[persistence.PublicationReconciliationJobArgs] {
	return &river.Job[persistence.PublicationReconciliationJobArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   persistence.PublicationReconciliationJobArgs{OperationID: operationID},
	}
}
