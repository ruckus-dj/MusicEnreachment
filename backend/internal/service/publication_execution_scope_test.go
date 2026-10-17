package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type scopeCommandFake struct {
	result PublicationCommandResult
	err    error
	calls  int
}

func (fake *scopeCommandFake) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	fake.calls++
	return fake.result, fake.err
}

type scopeRepositoryFake struct {
	valid                bool
	marked               bool
	checkErr             error
	markErr              error
	checks               int
	marks                int
	ctxCanceled          bool
	markErrorAfterCommit error
}

func (fake *scopeRepositoryFake) CheckPublicationDelivery(ctx context.Context, _ persistence.PublicationDeliveryGuard) (bool, error) {
	fake.checks++
	fake.ctxCanceled = ctx.Err() != nil
	return fake.valid, fake.checkErr
}

func (fake *scopeRepositoryFake) MarkPublicationQuiescent(ctx context.Context, _ persistence.PublicationDeliveryGuard) (bool, error) {
	fake.marks++
	fake.ctxCanceled = fake.ctxCanceled || ctx.Err() != nil
	if fake.markErrorAfterCommit != nil {
		err := fake.markErrorAfterCommit
		fake.markErrorAfterCommit = nil
		fake.marked = true
		return false, err
	}
	return fake.marked, fake.markErr
}

func newScopeForTest(t *testing.T, command PublicationCommandRunner, repository *scopeRepositoryFake) *PublicationExecutionScope {
	t.Helper()
	claim := func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
		return persistence.PublicationDeliveryAcquired, nil
	}
	scope, err := NewPublicationExecutionScope(context.Background(), publication.NewCoordinator(), []publication.ResourceLock{{Key: "position", Mode: publication.Exclusive}}, persistence.PublicationDeliveryGuard{}, claim, command, repository)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestPublicationScopeUnknownExecutionRetainsResourcesAndRejectsLaunch(t *testing.T) {
	command := &scopeCommandFake{result: PublicationCommandResult{Output: []byte("partial")}}
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, command, repository)
	if _, err := scope.Run(context.Background(), "/managed/tool"); err == nil {
		t.Fatal("unknown quiescence accepted")
	}
	if _, err := scope.Run(context.Background(), "/managed/tool"); err == nil {
		t.Fatal("launch accepted after unknown execution")
	}
	closed := false
	if err := scope.Finalize(context.Background(), func() error { closed = true; return nil }); err == nil {
		t.Fatal("unknown execution finalized")
	}
	if closed || command.calls != 1 || repository.checks != 0 || repository.marks != 0 {
		t.Fatalf("unsafe cleanup occurred: closed=%v calls=%d repo=%+v", closed, command.calls, repository)
	}
}

func TestPublicationScopeFailedButQuiescentCanFinalize(t *testing.T) {
	commandErr := errors.New("tool failed")
	command := &scopeCommandFake{result: PublicationCommandResult{Output: []byte("diagnostic"), QuiescenceEstablished: true}, err: commandErr}
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, command, repository)
	result, err := scope.Run(context.Background(), "/managed/tool")
	if !errors.Is(err, commandErr) || !result.QuiescenceEstablished {
		t.Fatalf("run result = %+v, %v", result, err)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if repository.checks != 0 || repository.marks != 1 {
		t.Fatalf("repository calls = checks %d marks %d", repository.checks, repository.marks)
	}
}

func TestPublicationScopeFalseDatabaseAcknowledgementRetainsResourcesAndCanRetry(t *testing.T) {
	command := &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{valid: true, marked: false}
	scope := newScopeForTest(t, command, repository)
	if _, err := scope.Run(context.Background(), "/managed/tool"); err != nil {
		t.Fatal(err)
	}
	closed := 0
	closeStage := func() error { closed++; return nil }
	if err := scope.Finalize(context.Background(), closeStage); err == nil {
		t.Fatal("false database acknowledgement accepted")
	}
	repository.marked = true
	if err := scope.Finalize(context.Background(), closeStage); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finalize(context.Background(), closeStage); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finalize(context.Background(), closeStage); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || repository.checks != 0 || repository.marks != 2 {
		t.Fatalf("unexpected retries: close=%d repo checks=%d marks=%d", closed, repository.checks, repository.marks)
	}
}

func TestPublicationScopeRetriesQuiescenceAfterCommitConnectionError(t *testing.T) {
	command := &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{
		valid:                true,
		markErrorAfterCommit: errors.New("connection lost after commit"),
	}
	scope := newScopeForTest(t, command, repository)
	if _, err := scope.Run(context.Background(), "/managed/tool"); err != nil {
		t.Fatal(err)
	}
	closed := 0
	closeStage := func() error { closed++; return nil }
	if err := scope.Finalize(context.Background(), closeStage); err == nil {
		t.Fatal("quiescence connection error was ignored")
	}
	// The state is already quiescent, so an independent live-delivery check
	// would reject it. Retrying the exact guarded, idempotent mark must succeed.
	repository.valid = false
	if err := scope.Finalize(context.Background(), closeStage); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || repository.checks != 0 || repository.marks != 2 {
		t.Fatalf("unexpected retries: close=%d repo checks=%d marks=%d", closed, repository.checks, repository.marks)
	}
}

func TestPublicationScopeFinalizationIgnoresCallerCancellation(t *testing.T) {
	command := &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, command, repository)
	if _, err := scope.Run(context.Background(), "/managed/tool"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.Finalize(ctx, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if repository.ctxCanceled {
		t.Fatal("cleanup inherited caller cancellation")
	}
}

func TestPublicationScopeCleanupFailureRetainsResources(t *testing.T) {
	command := &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, command, repository)
	if _, err := scope.Run(context.Background(), "/managed/tool"); err != nil {
		t.Fatal(err)
	}
	closeErr := errors.New("stage close failed")
	if err := scope.Finalize(context.Background(), func() error { return closeErr }); !errors.Is(err, closeErr) {
		t.Fatalf("finalize error = %v", err)
	}
	if repository.checks != 0 || repository.marks != 0 {
		t.Fatalf("quiescence attempted after cleanup failure: %+v", repository)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("failed close was silently retried or acknowledged")
	}
}

func TestPublicationScopeMissingBackendIsPrelaunchAndFinalizable(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, nil, repository)
	result, err := scope.Run(context.Background(), "/managed/tool")
	if err == nil || !result.QuiescenceEstablished {
		t.Fatalf("missing backend result = %+v, %v", result, err)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedPublicationOutputCollectorDrainsAndCopiesSnapshot(t *testing.T) {
	if _, err := NewBoundedOutputCollector(0); err == nil {
		t.Fatal("zero output limit accepted")
	}
	collector, err := NewBoundedOutputCollector(3)
	if err != nil {
		t.Fatal(err)
	}
	written, err := collector.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("write = %d, %v", written, err)
	}
	output, total, overflow := collector.Snapshot()
	if !reflect.DeepEqual(output, []byte("abc")) || total != 6 || !overflow {
		t.Fatalf("snapshot = %q, %d, %v", output, total, overflow)
	}
	output[0] = 'z'
	second, _, _ := collector.Snapshot()
	if string(second) != "abc" {
		t.Fatalf("snapshot aliases collector: %q", second)
	}
}
