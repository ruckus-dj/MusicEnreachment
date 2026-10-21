package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type publicationSourceCleanupRetryExecutorFunc func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error)

func (executor publicationSourceCleanupRetryExecutorFunc) ExecutePublicationSourceCleanupExplicitRetry(ctx context.Context, operationID uuid.UUID, jobID int64, attempt int) (service.PublicationSourceCleanupRunOutcome, error) {
	return executor(ctx, operationID, jobID, attempt)
}

func TestPublicationSourceCleanupRetryWorkerForwardsActualRiverIdentity(t *testing.T) {
	opID := uuid.New()
	called := false
	worker := NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(_ context.Context, gotID uuid.UUID, jobID int64, attempt int) (service.PublicationSourceCleanupRunOutcome, error) {
		called = true
		if gotID != opID || jobID != 87 || attempt != 4 {
			t.Fatalf("executor identity = %s %d %d, want %s 87 4", gotID, jobID, attempt, opID)
		}
		return service.PublicationSourceCleanupCompleted, nil
	}))
	if err := worker.Work(context.Background(), publicationSourceCleanupRetryTestJob(opID, 87, 4)); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("executor was not called")
	}
}

func TestPublicationSourceCleanupRetryArgsContainOnlyOperationID(t *testing.T) {
	opID := uuid.New()
	args := PublicationSourceCleanupRetryArgs{OperationID: opID}
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
	if got := args.Kind(); got != "retry_publication_sources_v1" {
		t.Fatalf("Kind() = %q", got)
	}
}

func TestPublicationSourceCleanupRetryWorkerOutcomeAcknowledgements(t *testing.T) {
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
			worker := NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
				return test.outcome, test.err
			}))
			err := worker.Work(context.Background(), publicationSourceCleanupRetryTestJob(uuid.New(), 91, 3))
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

func waitForCleanupRetryWorkers(t *testing.T, workers *sync.WaitGroup) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		workers.Wait()
		close(finished)
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		t.Error("cleanup retry worker goroutines did not stop after release and cancellation")
	}
}

func TestPublicationSourceCleanupRetryWorkerDoesNotAcknowledgeBeforeExecutorReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entered := make(chan struct{})
	allowReturn := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(allowReturn) }) }

	// Release and cancellation precede a separately bounded cleanup join.
	var wg sync.WaitGroup
	t.Cleanup(func() {
		release()
		cancel()
		waitForCleanupRetryWorkers(t, &wg)
	})

	worker := NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(ctx context.Context, _ uuid.UUID, _ int64, _ int) (service.PublicationSourceCleanupRunOutcome, error) {
		close(entered)
		select {
		case <-allowReturn:
		case <-ctx.Done():
			return service.PublicationSourceCleanupRecoveryRequired, ctx.Err()
		}
		return service.PublicationSourceCleanupRecoveryRequired, errors.New("native command failure")
	}))
	result := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		result <- worker.Work(ctx, publicationSourceCleanupRetryTestJob(uuid.New(), 100, 5))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("executor was not entered before timeout")
	}
	// The callback is still blocked on allowReturn, so Work cannot have returned.
	select {
	case err := <-result:
		t.Fatalf("Work returned before executor completion: %v", err)
	default:
	}
	release()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Work() error = %v, want ACK after executor return", err)
		}
	case <-ctx.Done():
		t.Fatal("Work did not return after executor release")
	}
}

func TestPublicationSourceCleanupRetryWorkerDuplicateBlockedWhileOwnerRuns(t *testing.T) {
	type forwarded struct {
		operationID uuid.UUID
		jobID       int64
		attempt     int
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	operationID := uuid.New()
	const jobID, attempt = 101, 2
	entered := make(chan forwarded, 2)
	allowReturn := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(allowReturn) }) }

	// Cleanup always unblocks the owner before joining, so no goroutine leaks even
	// when the test fails early; the join stays bounded by the timeout.
	var wg sync.WaitGroup
	t.Cleanup(func() {
		release()
		cancel()
		waitForCleanupRetryWorkers(t, &wg)
	})

	var mu sync.Mutex
	calls := 0
	worker := NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(ctx context.Context, gotOperationID uuid.UUID, gotJobID int64, gotAttempt int) (service.PublicationSourceCleanupRunOutcome, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		entered <- forwarded{operationID: gotOperationID, jobID: gotJobID, attempt: gotAttempt}
		if call == 1 {
			select {
			case <-allowReturn:
			case <-ctx.Done():
				return service.PublicationSourceCleanupRecoveryRequired, ctx.Err()
			}
			return service.PublicationSourceCleanupRecoveryRequired, errors.New("quiesced failure")
		}
		return service.PublicationSourceCleanupBlocked, nil
	}))
	firstResult := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstResult <- worker.Work(ctx, publicationSourceCleanupRetryTestJob(operationID, jobID, attempt))
	}()
	var owner forwarded
	select {
	case owner = <-entered:
	case <-ctx.Done():
		t.Fatal("owner executor was not entered before timeout")
	}
	if owner != (forwarded{operationID: operationID, jobID: jobID, attempt: attempt}) {
		t.Fatalf("owner forwarded delivery = %+v, want %s/%d/%d", owner, operationID, jobID, attempt)
	}

	// The duplicate is the exact same River delivery while the owner still holds
	// it: the executor sees identical metadata and reports the ordinary blocked
	// error, which must not be acknowledged while the owner is running. The
	// duplicate runs in a goroutine with a buffered result so a blocking
	// regression becomes a bounded test failure instead of a hang.
	duplicateResult := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		duplicateResult <- worker.Work(ctx, publicationSourceCleanupRetryTestJob(operationID, jobID, attempt))
	}()
	var duplicate forwarded
	select {
	case duplicate = <-entered:
	case <-ctx.Done():
		t.Fatal("duplicate executor was not entered before timeout")
	}
	if duplicate != owner {
		t.Fatalf("duplicate forwarded delivery = %+v, want %+v", duplicate, owner)
	}
	// The owner is still blocked on allowReturn, so neither Work may have returned.
	select {
	case err := <-firstResult:
		t.Fatalf("owner returned before release: %v", err)
	default:
	}
	var duplicateErr error
	select {
	case duplicateErr = <-duplicateResult:
	case <-ctx.Done():
		t.Fatal("duplicate blocked execution did not return")
	}
	if duplicateErr == nil {
		t.Fatal("duplicate blocked execution was acknowledged")
	}
	if !strings.Contains(duplicateErr.Error(), "blocked") {
		t.Fatalf("duplicate Work() error = %v, want ordinary blocked error", duplicateErr)
	}
	release()
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("owner Work() error = %v, want recovery ACK", err)
		}
	case <-ctx.Done():
		t.Fatal("owner did not return after release")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("executor calls = %d, want owner and one duplicate", calls)
	}
}

func TestPublicationSourceCleanupRetryWorkerRejectsIncompleteIdentityAndCancellation(t *testing.T) {
	opID := uuid.New()
	newJob := func() *river.Job[PublicationSourceCleanupRetryArgs] {
		return publicationSourceCleanupRetryTestJob(opID, 20, 2)
	}
	var nilWorker *PublicationSourceCleanupRetryWorker
	for _, test := range []struct {
		name   string
		worker *PublicationSourceCleanupRetryWorker
		job    *river.Job[PublicationSourceCleanupRetryArgs]
	}{
		{name: "nil worker", worker: nilWorker, job: newJob()},
		{name: "nil executor", worker: NewPublicationSourceCleanupRetryWorker(nil), job: newJob()},
		{name: "nil job"},
		{name: "nil job row", worker: NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(nil)), job: &river.Job[PublicationSourceCleanupRetryArgs]{Args: PublicationSourceCleanupRetryArgs{OperationID: opID}}},
		{name: "nil operation ID", worker: NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(nil)), job: publicationSourceCleanupRetryTestJob(uuid.Nil, 20, 2)},
		{name: "nonpositive job ID", worker: NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(nil)), job: publicationSourceCleanupRetryTestJob(opID, 0, 2)},
		{name: "nonpositive attempt", worker: NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(nil)), job: publicationSourceCleanupRetryTestJob(opID, 20, 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.worker == nil && test.name != "nil worker" {
				test.worker = NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
					return service.PublicationSourceCleanupCompleted, nil
				}))
			}
			if err := test.worker.Work(context.Background(), test.job); err == nil {
				t.Fatal("Work() error = nil, want invalid identity error")
			}
		})
	}

	calls := 0
	worker := NewPublicationSourceCleanupRetryWorker(publicationSourceCleanupRetryExecutorFunc(func(context.Context, uuid.UUID, int64, int) (service.PublicationSourceCleanupRunOutcome, error) {
		calls++
		return service.PublicationSourceCleanupCompleted, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Work(ctx, newJob()); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("Work() = %v, calls = %d; want canceled and no executor call", err, calls)
	}
}

func publicationSourceCleanupRetryTestJob(operationID uuid.UUID, jobID int64, attempt int) *river.Job[PublicationSourceCleanupRetryArgs] {
	return &river.Job[PublicationSourceCleanupRetryArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt},
		Args:   PublicationSourceCleanupRetryArgs{OperationID: operationID},
	}
}
