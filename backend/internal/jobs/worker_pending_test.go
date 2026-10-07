package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// recordingPendingDispatcher records the roots it is asked to admit. It is a
// single-method stand-in, not a mock of the worker's repository.
type recordingPendingDispatcher struct {
	roots []uuid.UUID
	err   error
}

func (dispatcher *recordingPendingDispatcher) AdmitPending(_ context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	dispatcher.roots = append(dispatcher.roots, rootID)
	return nil, dispatcher.err
}

func TestSourceAnalysisWorkerWakePendingAdmitsOnlyKnownRoot(t *testing.T) {
	dispatcher := &recordingPendingDispatcher{}
	worker := &SourceAnalysisWorker{}
	worker.SetPendingDispatcher(dispatcher)
	ctx := context.Background()

	// Neither a missing root target nor a nil operation may reach the dispatcher.
	worker.wakePending(ctx, &persistence.Operation{ID: uuid.New()})
	worker.wakePending(ctx, nil)
	if len(dispatcher.roots) != 0 {
		t.Fatalf("wakePending admitted %d roots without a known root", len(dispatcher.roots))
	}

	rootID := uuid.New()
	worker.wakePending(ctx, &persistence.Operation{ID: uuid.New(), TargetSourceRootID: &rootID})
	if len(dispatcher.roots) != 1 || dispatcher.roots[0] != rootID {
		t.Fatalf("wakePending roots = %v, want [%s]", dispatcher.roots, rootID)
	}
}

func TestSourceAnalysisWorkerWakePendingSwallowsAdmissionError(t *testing.T) {
	rootID := uuid.New()
	dispatcher := &recordingPendingDispatcher{err: errors.New("admission unavailable")}
	worker := &SourceAnalysisWorker{}
	worker.SetPendingDispatcher(dispatcher)

	// A downstream admission error must be contained by the helper and never
	// surface to the caller that already committed the terminal settlement.
	worker.wakePending(context.Background(), &persistence.Operation{ID: uuid.New(), TargetSourceRootID: &rootID})
	if len(dispatcher.roots) != 1 {
		t.Fatalf("wakePending admitted %d roots, want 1", len(dispatcher.roots))
	}
}

func TestSourceAnalysisWorkerWakePendingWithoutDispatcherIsNoop(t *testing.T) {
	rootID := uuid.New()
	worker := &SourceAnalysisWorker{}
	worker.wakePending(context.Background(), &persistence.Operation{ID: uuid.New(), TargetSourceRootID: &rootID})
}
