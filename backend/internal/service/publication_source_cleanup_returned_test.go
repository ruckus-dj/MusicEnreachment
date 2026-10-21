package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type returnedCleanupRepository struct {
	*publicationSourceCleanupFake
	failCalls chan failCleanupCall
}

type failCleanupCall struct {
	guard persistence.PublicationSourceCleanupDeliveryGuard
	safe  string
}

func (repository *returnedCleanupRepository) FailPublicationSourceCleanupDelivery(_ context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard, safe string) (bool, error) {
	repository.failCalls <- failCleanupCall{guard: guard, safe: safe}
	return true, nil
}

type blockingCleanupRemover struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (remover *blockingCleanupRemover) Remove(context.Context, string, string, int64, time.Time) error {
	close(remover.entered)
	<-remover.release
	return remover.err
}

func TestPublicationSourceCleanupRunnerFailsOnlyAfterNativeRemovalReturns(t *testing.T) {
	nativeErr := errors.New("native removal failed")
	base := validPublicationSourceCleanupFake()
	base.input.ItemIDs = base.input.ItemIDs[:2]
	repository := &returnedCleanupRepository{publicationSourceCleanupFake: base, failCalls: make(chan failCleanupCall, 1)}
	remover := &blockingCleanupRemover{entered: make(chan struct{}), release: make(chan struct{}), err: nativeErr}
	runner, err := NewPublicationSourceCleanupRunner(repository, remover, uuid.New())
	if err != nil {
		t.Fatalf("NewPublicationSourceCleanupRunner() error = %v", err)
	}
	type runResult struct {
		outcome PublicationSourceCleanupRunOutcome
		err     error
	}
	result := make(chan runResult, 1)
	go func() {
		outcome, err := runner.ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 4)
		result <- runResult{outcome: outcome, err: err}
	}()

	<-remover.entered
	select {
	case call := <-repository.failCalls:
		t.Fatalf("failure recorded before native removal returned: %+v", call)
	default:
	}
	select {
	case got := <-result:
		t.Fatalf("runner completed before native removal returned: %+v", got)
	default:
	}

	close(remover.release)
	got := <-result
	var call failCleanupCall
	select {
	case call = <-repository.failCalls:
	default:
		t.Fatalf("runner finished without recording failure: %+v", got)
	}
	if got.outcome != PublicationSourceCleanupRecoveryRequired || !errors.Is(got.err, nativeErr) {
		t.Fatalf("Execute() = %q, %v", got.outcome, got.err)
	}
	if call.safe != publicationSourceCleanupSafeError {
		t.Fatalf("failure safe error = %q", call.safe)
	}
	if len(base.guards) == 0 || call.guard != base.guards[0] {
		t.Fatalf("failure guard = %+v; removal guard = %+v", call.guard, base.guards)
	}
	if len(base.itemIDs) != 1 || base.itemIDs[0] != base.input.ItemIDs[0] {
		t.Fatalf("runner continued past failed item: removed item IDs %v", base.itemIDs)
	}
	if len(base.trace) == 0 || base.trace[len(base.trace)-1] != "remove" {
		t.Fatalf("unexpected repository transitions after failure: %v", base.trace)
	}
	select {
	case extra := <-repository.failCalls:
		t.Fatalf("failure recorded more than once: %+v", extra)
	default:
	}
}
