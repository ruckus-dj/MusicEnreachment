package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type pendingAdmissionFixture struct {
	calls  int
	rootID uuid.UUID
	err    error
}

func (fixture *pendingAdmissionFixture) AdmitPending(_ context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	fixture.calls++
	fixture.rootID = rootID
	return nil, fixture.err
}

type failingTransitionRepository struct {
	*memoryOperations
	err error
}

func (repository *failingTransitionRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	if repository.err != nil {
		return repository.err
	}
	return repository.memoryOperations.TransitionOperation(ctx, id, transition)
}

func TestOperationsAdmitsPendingAfterSourceRootTerminalTransition(t *testing.T) {
	ctx := context.Background()
	rootID := uuid.New()
	for _, state := range []string{"succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			operation := &persistence.Operation{ID: uuid.New(), Kind: SourceScanOperationKind, State: "running", TargetSourceRootID: &rootID}
			repository := &memoryOperations{values: map[uuid.UUID]*persistence.Operation{operation.ID: operation}}
			dispatcher := &pendingAdmissionFixture{}
			operations := NewOperations(repository)
			operations.SetPendingDispatcher(dispatcher)

			var err error
			if state == "succeeded" {
				err = operations.Succeed(ctx, operation.ID, "succeeded")
			} else {
				err = operations.Fail(ctx, operation.ID, "scan", "safe failure")
			}
			if err != nil {
				t.Fatalf("transition operation: %v", err)
			}
			if dispatcher.calls != 1 || dispatcher.rootID != rootID {
				t.Fatalf("pending admission = (%d calls, root %s), want (1, %s)", dispatcher.calls, dispatcher.rootID, rootID)
			}
		})
	}
}

func TestOperationsDoesNotAdmitPendingForProgressOrFailedTransition(t *testing.T) {
	ctx := context.Background()
	rootID := uuid.New()
	operation := &persistence.Operation{ID: uuid.New(), Kind: SourceScanOperationKind, State: "running", TargetSourceRootID: &rootID}
	repository := &failingTransitionRepository{
		memoryOperations: &memoryOperations{values: map[uuid.UUID]*persistence.Operation{operation.ID: operation}},
		err:              errors.New("transition failed"),
	}
	dispatcher := &pendingAdmissionFixture{}
	operations := NewOperations(repository)
	operations.SetPendingDispatcher(dispatcher)
	if err := operations.Progress(ctx, operation.ID, "traversing", 1, nil); err == nil {
		t.Fatal("Progress unexpectedly succeeded")
	}
	if dispatcher.calls != 0 {
		t.Fatalf("pending admission calls after failed transition = %d, want 0", dispatcher.calls)
	}

	repository.err = nil
	if err := operations.Progress(ctx, operation.ID, "traversing", 1, nil); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("pending admission calls after ordinary progress = %d, want 0", dispatcher.calls)
	}
}

func TestOperationsPendingAdmissionFailureDoesNotUndoCommittedTerminalState(t *testing.T) {
	ctx := context.Background()
	rootID := uuid.New()
	operation := &persistence.Operation{ID: uuid.New(), Kind: SourceScanOperationKind, State: "running", TargetSourceRootID: &rootID}
	repository := &memoryOperations{values: map[uuid.UUID]*persistence.Operation{operation.ID: operation}}
	dispatcher := &pendingAdmissionFixture{err: errors.New("pending admission failed")}
	operations := NewOperations(repository)
	operations.SetPendingDispatcher(dispatcher)

	if err := operations.Succeed(ctx, operation.ID, "succeeded"); err != nil {
		t.Fatalf("succeed with downstream admission failure: %v", err)
	}
	if operation.State != "succeeded" {
		t.Fatalf("operation state = %q, want succeeded", operation.State)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("pending admission calls = %d, want 1", dispatcher.calls)
	}
}
