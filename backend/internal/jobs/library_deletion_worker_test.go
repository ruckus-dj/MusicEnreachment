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

type libraryDeletionExecutorFake struct {
	operationID uuid.UUID
	jobID       int64
	attempt     int
	executeErr  error
	handled     bool
	lookupErr   error
	lookupCalls int
}

func (executor *libraryDeletionExecutorFake) ExecuteDelivery(_ context.Context, operationID uuid.UUID, jobID int64, attempt int) error {
	executor.operationID, executor.jobID, executor.attempt = operationID, jobID, attempt
	return executor.executeErr
}

func (executor *libraryDeletionExecutorFake) DeliveryFailureWasRecorded(_ context.Context, operationID uuid.UUID, jobID int64) (bool, error) {
	executor.lookupCalls++
	if operationID != executor.operationID || jobID != executor.jobID {
		return false, errors.New("failure lookup received a different delivery identity")
	}
	return executor.handled, executor.lookupErr
}

type libraryDeletionNotifierFake struct {
	operationID uuid.UUID
	calls       int
}

func (notifier *libraryDeletionNotifierFake) Notify(operationID uuid.UUID) {
	notifier.operationID = operationID
	notifier.calls++
}

func TestLibraryDeletionWorkerForwardsRiverIdentityAndNotifies(t *testing.T) {
	operationID := uuid.New()
	executor := &libraryDeletionExecutorFake{}
	notifier := &libraryDeletionNotifierFake{}
	worker := NewLibraryDeletionWorker(executor, notifier)
	if err := worker.Work(context.Background(), libraryDeletionWorkerTestJob(operationID, 76, 3)); err != nil {
		t.Fatal(err)
	}
	if executor.operationID != operationID || executor.jobID != 76 || executor.attempt != 3 {
		t.Fatalf("executor received %s/%d/%d, want %s/76/3", executor.operationID, executor.jobID, executor.attempt, operationID)
	}
	if notifier.calls != 1 || notifier.operationID != operationID {
		t.Fatalf("operation notification = %s, calls=%d; want one notification for %s", notifier.operationID, notifier.calls, operationID)
	}
}

func TestLibraryDeletionWorkerAcknowledgesOnlyDurablyRecordedFailure(t *testing.T) {
	executionFailure := errors.New("native output changed")
	for _, test := range []struct {
		name    string
		handled bool
		wantErr bool
	}{
		{name: "durable failure is handled", handled: true},
		{name: "unrecorded failure returns to River", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &libraryDeletionExecutorFake{executeErr: executionFailure, handled: test.handled}
			worker := NewLibraryDeletionWorker(executor, nil)
			err := worker.Work(context.Background(), libraryDeletionWorkerTestJob(uuid.New(), 81, 2))
			if executor.lookupCalls != 1 {
				t.Fatalf("durable failure state checks = %d, want 1 before deciding River ACK", executor.lookupCalls)
			}
			if test.wantErr && !errors.Is(err, executionFailure) {
				t.Fatalf("Work() error = %v, want wrapped execution failure", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Work() error = %v, want ACK after durable application failure", err)
			}
		})
	}

	lookupFailure := errors.New("database unavailable")
	executor := &libraryDeletionExecutorFake{executeErr: executionFailure, lookupErr: lookupFailure}
	worker := NewLibraryDeletionWorker(executor, nil)
	if err := worker.Work(context.Background(), libraryDeletionWorkerTestJob(uuid.New(), 82, 1)); !errors.Is(err, lookupFailure) || !errors.Is(err, executionFailure) {
		t.Fatalf("failure state lookup error = %v, want both execution and lookup causes", err)
	}
}

func libraryDeletionWorkerTestJob(operationID uuid.UUID, jobID int64, attempt int) *river.Job[persistence.LibraryDeletionOperationArgs] {
	return &river.Job[persistence.LibraryDeletionOperationArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   persistence.LibraryDeletionOperationArgs{OperationID: operationID},
	}
}
