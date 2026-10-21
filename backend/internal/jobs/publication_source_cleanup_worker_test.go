package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type publicationSourceCleanupExecutorFunc func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error)

func (executor publicationSourceCleanupExecutorFunc) ExecutePublicationSourceCleanupDelivery(ctx context.Context, operationID uuid.UUID, jobID int64, attempt int) (service.PublicationSourceCleanupRunOutcome, error) {
	return executor(ctx, operationID, jobID, attempt)
}

func TestPublicationSourceCleanupWorkerForwardsActualRiverIdentity(t *testing.T) {
	opID := uuid.New()
	called := false
	worker := NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(_ context.Context, gotID uuid.UUID, jobID int64, attempt int) (service.PublicationSourceCleanupRunOutcome, error) {
		called = true
		if gotID != opID || jobID != 87 || attempt != 4 {
			t.Fatalf("executor identity = %s %d %d, want %s 87 4", gotID, jobID, attempt, opID)
		}
		return service.PublicationSourceCleanupCompleted, nil
	}))
	if err := worker.Work(context.Background(), publicationSourceCleanupTestJob(opID, 87, 4)); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("executor was not called")
	}
}

func TestPublicationSourceCleanupArgsContainOnlyOperationID(t *testing.T) {
	opID := uuid.New()
	args := PublicationSourceCleanupArgs{OperationID: opID}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || string(fields["operation_id"]) != `"`+opID.String()+`"` {
		t.Fatalf("serialized args = %s, want only operation_id", encoded)
	}
	if got := args.Kind(); got != "cleanup_publication_sources_v1" {
		t.Fatalf("Kind() = %q", got)
	}
}

func TestPublicationSourceCleanupWorkerOutcomeAcknowledgements(t *testing.T) {
	diagnostic := errors.New("service diagnostic")
	for _, test := range []struct {
		name    string
		outcome service.PublicationSourceCleanupRunOutcome
		err     error
		wantErr error
		absent  string
	}{
		{name: "completed", outcome: service.PublicationSourceCleanupCompleted},
		{name: "completed preserves error", outcome: service.PublicationSourceCleanupCompleted, err: diagnostic, wantErr: diagnostic},
		{name: "cancelled", outcome: service.PublicationSourceCleanupCancelled},
		{name: "cancelled with committed diagnostic", outcome: service.PublicationSourceCleanupCancelled, err: diagnostic},
		{name: "failed", outcome: service.PublicationSourceCleanupFailed},
		{name: "failed with committed diagnostic", outcome: service.PublicationSourceCleanupFailed, err: diagnostic},
		{name: "recovery required", outcome: service.PublicationSourceCleanupRecoveryRequired},
		{name: "recovery required diagnostic", outcome: service.PublicationSourceCleanupRecoveryRequired, err: diagnostic},
		{name: "blocked", outcome: service.PublicationSourceCleanupBlocked, wantErr: errors.New("blocked")},
		{name: "already owned collapses to blocked", outcome: service.PublicationSourceCleanupBlocked, wantErr: errors.New("blocked"), absent: "already owned"},
		{name: "blocked preserves error", outcome: service.PublicationSourceCleanupBlocked, err: diagnostic, wantErr: diagnostic},
		{name: "unknown", outcome: "unknown", wantErr: errors.New("unknown outcome")},
		{name: "empty", wantErr: errors.New("unknown outcome")},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker := NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
				return test.outcome, test.err
			}))
			err := worker.Work(context.Background(), publicationSourceCleanupTestJob(uuid.New(), 91, 3))
			if test.wantErr == nil && err != nil {
				t.Fatalf("Work() error = %v, want nil", err)
			}
			if test.wantErr != nil && err == nil {
				t.Fatal("Work() error = nil, want non-nil")
			}
			if test.err != nil && test.outcome == service.PublicationSourceCleanupBlocked && !errors.Is(err, test.err) {
				t.Fatalf("Work() error %v does not preserve %v", err, test.err)
			}
			if test.outcome == service.PublicationSourceCleanupCompleted && test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("Work() error %v does not preserve %v", err, test.err)
			}
			if test.wantErr != nil && test.err == nil && !strings.Contains(err.Error(), test.wantErr.Error()) {
				t.Fatalf("Work() error = %v, want diagnostic containing %q", err, test.wantErr)
			}
			if test.absent != "" && err != nil && strings.Contains(err.Error(), test.absent) {
				t.Fatalf("Work() error = %v, want diagnostic without %q", err, test.absent)
			}
		})
	}
}

func TestPublicationSourceCleanupWorkerDoesNotAcknowledgeBeforeExecutorReturns(t *testing.T) {
	entered := make(chan struct{})
	allowReturn := make(chan struct{})
	worker := NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
		close(entered)
		<-allowReturn
		return service.PublicationSourceCleanupRecoveryRequired, errors.New("native command failure")
	}))
	result := make(chan error, 1)
	go func() {
		result <- worker.Work(context.Background(), publicationSourceCleanupTestJob(uuid.New(), 100, 5))
	}()
	<-entered
	select {
	case err := <-result:
		t.Fatalf("Work returned before executor completion: %v", err)
	default:
	}
	close(allowReturn)
	if err := <-result; err != nil {
		t.Fatalf("Work() error = %v, want ACK after executor return", err)
	}
}

func TestPublicationSourceCleanupWorkerDuplicateBlockedWhileOwnerRuns(t *testing.T) {
	type forwarded struct {
		operationID uuid.UUID
		jobID       int64
		attempt     int
	}
	operationID := uuid.New()
	const jobID, attempt = 101, 2
	entered := make(chan forwarded, 2)
	allowReturn := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	worker := NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(_ context.Context, gotOperationID uuid.UUID, gotJobID int64, gotAttempt int) (service.PublicationSourceCleanupRunOutcome, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		entered <- forwarded{operationID: gotOperationID, jobID: gotJobID, attempt: gotAttempt}
		if call == 1 {
			<-allowReturn
			return service.PublicationSourceCleanupRecoveryRequired, errors.New("quiesced failure")
		}
		return service.PublicationSourceCleanupBlocked, nil
	}))
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- worker.Work(context.Background(), publicationSourceCleanupTestJob(operationID, jobID, attempt))
	}()
	owner := <-entered
	if owner != (forwarded{operationID: operationID, jobID: jobID, attempt: attempt}) {
		t.Fatalf("owner forwarded delivery = %+v, want %s/%d/%d", owner, operationID, jobID, attempt)
	}

	// The duplicate is the exact same River delivery while the owner still holds
	// it: the executor sees identical metadata and reports the ordinary blocked
	// error, which must not be acknowledged while the owner is running.
	duplicateErr := worker.Work(context.Background(), publicationSourceCleanupTestJob(operationID, jobID, attempt))
	duplicate := <-entered
	if duplicate != owner {
		t.Fatalf("duplicate forwarded delivery = %+v, want %+v", duplicate, owner)
	}
	if duplicateErr == nil {
		t.Fatal("duplicate blocked execution was acknowledged")
	}
	if !strings.Contains(duplicateErr.Error(), "blocked") {
		t.Fatalf("duplicate Work() error = %v, want ordinary blocked error", duplicateErr)
	}
	select {
	case err := <-firstResult:
		t.Fatalf("owner returned before release: %v", err)
	default:
	}
	close(allowReturn)
	if err := <-firstResult; err != nil {
		t.Fatalf("owner Work() error = %v, want recovery ACK", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("executor calls = %d, want owner and one duplicate", calls)
	}
}

func TestPublicationSourceCleanupWorkerRejectsIncompleteIdentityAndCancellation(t *testing.T) {
	opID := uuid.New()
	newJob := func() *river.Job[PublicationSourceCleanupArgs] { return publicationSourceCleanupTestJob(opID, 20, 2) }
	var nilWorker *PublicationSourceCleanupWorker
	for _, test := range []struct {
		name   string
		worker *PublicationSourceCleanupWorker
		job    *river.Job[PublicationSourceCleanupArgs]
	}{
		{name: "nil worker", worker: nilWorker, job: newJob()},
		{name: "nil executor", worker: NewPublicationSourceCleanupWorker(nil), job: newJob()},
		{name: "nil job"},
		{name: "nil job row", worker: NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(nil)), job: &river.Job[PublicationSourceCleanupArgs]{Args: PublicationSourceCleanupArgs{OperationID: opID}}},
		{name: "nil operation ID", worker: NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(nil)), job: publicationSourceCleanupTestJob(uuid.Nil, 20, 2)},
		{name: "nonpositive job ID", worker: NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(nil)), job: publicationSourceCleanupTestJob(opID, 0, 2)},
		{name: "nonpositive attempt", worker: NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(nil)), job: publicationSourceCleanupTestJob(opID, 20, 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.worker == nil && test.name != "nil worker" {
				test.worker = NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
					return service.PublicationSourceCleanupCompleted, nil
				}))
			}
			if err := test.worker.Work(context.Background(), test.job); err == nil {
				t.Fatal("Work() error = nil, want invalid identity error")
			}
		})
	}

	calls := 0
	worker := NewPublicationSourceCleanupWorker(publicationSourceCleanupExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
		calls++
		return service.PublicationSourceCleanupCompleted, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Work(ctx, newJob()); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("Work() = %v, calls = %d; want canceled and no executor call", err, calls)
	}
}

func publicationSourceCleanupTestJob(operationID uuid.UUID, jobID int64, attempt int) *river.Job[PublicationSourceCleanupArgs] {
	return &river.Job[PublicationSourceCleanupArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   PublicationSourceCleanupArgs{OperationID: operationID},
	}
}
