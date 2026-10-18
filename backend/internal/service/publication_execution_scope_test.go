package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type scopeFile struct {
	file   *os.File
	borrow func(func(*os.File) error) error
}

func (input scopeFile) Stat(context.Context) (fs.FileInfo, error) { return input.file.Stat() }
func (input scopeFile) Borrow(_ context.Context, callback func(*os.File) error) error {
	if input.borrow != nil {
		return input.borrow(callback)
	}
	return callback(input.file)
}
func (input scopeFile) Close() error { return input.file.Close() }

type scopeFileCommandFake struct {
	result  PublicationCommandResult
	request tools.FileCommandRequest
	calls   int
	panic   bool
	run     func()
}

func (*scopeFileCommandFake) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}
func (fake *scopeFileCommandFake) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	fake.calls++
	fake.request = request
	if fake.panic {
		panic("runner panic")
	}
	if fake.run != nil {
		fake.run()
	}
	return fake.result, nil
}

var _ sourcefs.RegularFile = scopeFile{}

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

func TestPublicationScopeFinalizeWaitsForSynchronousReservation(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{}, repository)
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("synchronous work reservation was rejected")
	}
	closed := false
	if err := scope.Finalize(context.Background(), func() error { closed = true; return nil }); err == nil {
		t.Fatal("finalize succeeded while synchronous work was reserved")
	}
	if closed || repository.marks != 0 {
		t.Fatalf("finalized during synchronous work: closed=%v marks=%d", closed, repository.marks)
	}
	scope.releaseSynchronousWork(reservation)
}

func TestPublicationScopeReplacementReservationBorrowsOnlyItsOuterReservation(t *testing.T) {
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	outer := scope.reserveSynchronousWork()
	if outer == nil {
		t.Fatal("outer reservation was rejected")
	}
	borrowed, owned := scope.reserveReplacementWork(outer)
	if borrowed != outer || owned {
		t.Fatal("replacement did not borrow the live outer reservation")
	}
	if unrelated, _ := scope.reserveReplacementWork(nil); unrelated != nil {
		t.Fatal("replacement borrowed an unrelated synchronous reservation")
	}
	scope.releaseReplacementWork(borrowed, false)
	scope.releaseSynchronousWork(outer)
	if borrowed, owned := scope.reserveReplacementWork(outer); borrowed != nil || owned {
		t.Fatal("replacement borrowed a released reservation")
	}
	if ownedReservation, owned := scope.reserveReplacementWork(nil); ownedReservation == nil || !owned {
		t.Fatal("replacement could not reserve an idle scope")
	} else {
		scope.releaseSynchronousWork(ownedReservation)
	}
}

func TestPublicationScopeFirstAbsentFinalizationRejectsForeignOrBorrowedReservation(t *testing.T) {
	t.Run("foreign", func(t *testing.T) {
		scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
		foreignScope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
		reservation := scope.reserveSynchronousWork()
		foreign := foreignScope.reserveSynchronousWork()
		if err := scope.beginFirstAbsentFinalization(foreign); err == nil {
			t.Fatal("first-absent finalization accepted a foreign reservation")
		}
		scope.releaseSynchronousWork(reservation)
		foreignScope.releaseSynchronousWork(foreign)
	})

	t.Run("borrowed", func(t *testing.T) {
		scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
		outer := scope.reserveSynchronousWork()
		borrowed, owned := scope.reserveReplacementWork(outer)
		if borrowed != outer || owned {
			t.Fatal("replacement did not borrow the outer reservation")
		}
		if err := scope.beginFirstAbsentFinalization(borrowed); err == nil {
			t.Fatal("first-absent finalization accepted a borrowed reservation")
		}
		scope.releaseReplacementWork(borrowed, false)
		scope.releaseSynchronousWork(outer)
	})
}

func TestPublicationScopeFirstAbsentFenceBlocksLegacyFinalizeUntilAcknowledged(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{}, repository)
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("synchronous work reservation was rejected")
	}
	if err := scope.beginFirstAbsentFinalization(reservation); err != nil {
		t.Fatal(err)
	}
	closed := false
	if err := scope.Finalize(context.Background(), func() error { closed = true; return nil }); err == nil {
		t.Fatal("legacy finalize succeeded while first-absent cleanup owned the scope")
	}
	if closed || repository.marks != 0 {
		t.Fatalf("legacy finalize crossed first-absent fence: closed=%v marks=%d", closed, repository.marks)
	}
	scope.acknowledgeFirstAbsentFinalization()
	if !scope.finalized || scope.release != nil {
		t.Fatal("acknowledged first-absent completion did not release local ownership")
	}
}

func TestPublicationScopeFirstAbsentPanicQuarantineRetainsOwnership(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{}, repository)
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("synchronous work reservation was rejected")
	}
	if err := scope.beginFirstAbsentFinalization(reservation); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected cleanup panic")
		}
		scope.quarantineUncertain()
		closed := false
		if err := scope.Finalize(context.Background(), func() error { closed = true; return nil }); err == nil {
			t.Fatal("quarantined cleanup finalized")
		}
		if closed || repository.marks != 0 || scope.release == nil {
			t.Fatalf("quarantined cleanup released ownership: closed=%v marks=%d", closed, repository.marks)
		}
	}()
	panic("cleanup")
}

func TestPublicationScopeFileRunQuarantinesOwnedDuplicate(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("descriptor remains usable"); err != nil {
		t.Fatal(err)
	}
	command := &scopeFileCommandFake{}
	scope := newScopeForTest(t, command, &scopeRepositoryFake{marked: true})
	t.Cleanup(func() { closeTestScopeOwnedFiles(t, scope) })
	if _, err := scope.RunFile(context.Background(), "tool", []string{"arg"}, scopeFile{file: file}); err == nil {
		t.Fatal("unknown file command quiescence accepted")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := command.request.Input.Stat(); err != nil {
		t.Fatalf("quarantined command descriptor was closed: %v", err)
	}
	if command.calls != 1 || command.request.Executable != "tool" || len(command.request.Args) != 1 {
		t.Fatalf("unexpected file command request: %+v", command.request)
	}
	if _, err := scope.RunFile(context.Background(), "tool", nil, scopeFile{file: file}); err == nil {
		t.Fatal("launch accepted after unknown file command")
	}
}

func TestPublicationScopeFileRunClosesDuplicateAfterQuiescence(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	command := &scopeFileCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	scope := newScopeForTest(t, command, &scopeRepositoryFake{marked: true})
	if _, err := scope.RunFile(context.Background(), "tool", nil, scopeFile{file: file}); err != nil {
		t.Fatal(err)
	}
	if _, err := command.request.Input.Stat(); err == nil {
		t.Fatal("command duplicate remained usable after close")
	}
	if !scope.CanExecute() {
		t.Fatal("quiescent scope was fenced")
	}
}

func TestPublicationScopeFileBorrowPanicsQuarantineReservation(t *testing.T) {
	for _, afterCallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_callback_%v", afterCallback), func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "input")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := file.Close(); err != nil {
					t.Errorf("close input: %v", err)
				}
			})
			command := &scopeFileCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
			repository := &scopeRepositoryFake{marked: true}
			scope := newScopeForTest(t, command, repository)
			t.Cleanup(func() { closeTestScopeOwnedFiles(t, scope) })
			input := scopeFile{file: file, borrow: func(callback func(*os.File) error) error {
				if afterCallback {
					if err := callback(file); err != nil {
						return err
					}
				}
				panic("borrow panic")
			}}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("borrow panic was swallowed")
					}
				}()
				_, _ = scope.RunFile(context.Background(), "tool", nil, input)
			}()
			if command.calls != 0 || scope.CanExecute() || scope.unresolved != 0 || len(scope.ownedFiles) != boolInt(afterCallback) {
				t.Fatalf("panic state: calls=%d executable=%v unresolved=%d owned=%d", command.calls, scope.CanExecute(), scope.unresolved, len(scope.ownedFiles))
			}
			closed := false
			if err := scope.Finalize(context.Background(), func() error { closed = true; return nil }); err == nil || closed || repository.marks != 0 {
				t.Fatalf("unsafe finalization: err=%v closed=%v marks=%d", err, closed, repository.marks)
			}
		})
	}
}

func TestPublicationScopeFileRunnerPanicQuarantinesDuplicate(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	command := &scopeFileCommandFake{panic: true}
	scope := newScopeForTest(t, command, &scopeRepositoryFake{marked: true})
	t.Cleanup(func() { closeTestScopeOwnedFiles(t, scope) })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("runner panic was swallowed")
			}
		}()
		_, _ = scope.RunFile(context.Background(), "tool", nil, scopeFile{file: file})
	}()
	if command.calls != 1 || scope.CanExecute() || scope.unresolved != 0 {
		t.Fatalf("panic state: calls=%d executable=%v unresolved=%d", command.calls, scope.CanExecute(), scope.unresolved)
	}
	if _, err := command.request.Input.Stat(); err != nil {
		t.Fatalf("runner panic duplicate was not retained: %v", err)
	}
}

func closeTestScopeOwnedFiles(t *testing.T, scope *PublicationExecutionScope) {
	t.Helper()
	scope.mu.Lock()
	owned := append([]*os.File(nil), scope.ownedFiles...)
	scope.mu.Unlock()
	for _, file := range owned {
		if err := file.Close(); err != nil {
			t.Errorf("close retained publication command input: %v", err)
		}
	}
}

func TestPublicationScopeFileBorrowErrorIsPrelaunchAndFinalizable(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	borrowErr := errors.New("borrow failed")
	command := &scopeFileCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{marked: true}
	scope := newScopeForTest(t, command, repository)
	_, err = scope.RunFile(context.Background(), "tool", nil, scopeFile{file: file, borrow: func(func(*os.File) error) error { return borrowErr }})
	if !errors.Is(err, borrowErr) || command.calls != 0 || !scope.CanExecute() {
		t.Fatalf("borrow result: err=%v calls=%d executable=%v", err, command.calls, scope.CanExecute())
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationScopeFileMissingBackendDoesNotBorrow(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	repository := &scopeRepositoryFake{marked: true}
	scope := newScopeForTest(t, nil, repository)
	borrowed := false
	input := scopeFile{file: file, borrow: func(func(*os.File) error) error { borrowed = true; return nil }}
	result, err := scope.RunFile(context.Background(), "tool", nil, input)
	if err == nil || !result.QuiescenceEstablished || borrowed {
		t.Fatalf("missing backend result=%+v err=%v borrowed=%v", result, err, borrowed)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationScopeFinalizeWaitsForFileRunReservation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	entered, resume := make(chan struct{}), make(chan struct{})
	repository := &scopeRepositoryFake{marked: true}
	command := &scopeFileCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}, run: func() {
		close(entered)
		<-resume
	}}
	scope := newScopeForTest(t, command, repository)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := scope.RunFile(context.Background(), "tool", nil, scopeFile{file: file})
		runDone <- runErr
	}()
	<-entered
	closed := false
	closeStage := func() error { closed = true; return nil }
	if err := scope.Finalize(context.Background(), closeStage); err == nil || closed || repository.marks != 0 {
		t.Fatalf("finalized during run: err=%v closed=%v marks=%d", err, closed, repository.marks)
	}
	close(resume)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := scope.Finalize(context.Background(), closeStage); err != nil {
		t.Fatal(err)
	}
	if !closed || repository.marks != 1 {
		t.Fatalf("retry finalization: closed=%v marks=%d", closed, repository.marks)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
