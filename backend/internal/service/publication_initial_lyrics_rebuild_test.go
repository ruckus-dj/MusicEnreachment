package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type initialLyricsRebuildTestJournal struct {
	PublicationStagePreparationRepository
	outcome persistence.PublicationOptionalLyricsOutcome
	started chan struct{}
	resume  chan struct{}
}

func (journal *initialLyricsRebuildTestJournal) GuardedRecordPublicationInitialLyricsRebuild(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationCandidateRebuildRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	if journal.started != nil {
		close(journal.started)
		<-journal.resume
	}
	return journal.outcome, nil
}

func (*initialLyricsRebuildTestJournal) GuardedRecordPublicationInitialLyricsAcquisition(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialLyricsAcquisitionRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	return persistence.PublicationOptionalLyricsRecorded, nil
}

func (*initialLyricsRebuildTestJournal) GuardedRecordPublicationInitialLyricsOutcome(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialLyricsOutcomeRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	return persistence.PublicationOptionalLyricsRecorded, nil
}

func initialLyricsRebuildFixture(t *testing.T) (publicationStageFixture, *preparedPublicationStage, *PublicationExecutionScope, *publicationSyncReservation, []byte) {
	t.Helper()
	fixture := newPublicationStageFixture(t)
	prepared, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.(*preparedPublicationStage)
	t.Cleanup(func() { _ = stage.Close(); _ = fixture.preparer.OutputParent.Close() })
	input, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := stage.directory.CreateExclusive(context.Background(), filepath.Base(stage.paths.OutputPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write(context.Background(), []byte("initial candidate")); err != nil {
		t.Fatal(errors.Join(err, output.Close()))
	}
	if err := errors.Join(output.Sync(context.Background()), output.Close()); err != nil {
		t.Fatal(err)
	}
	scope := newScopeForTest(t, &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}, &scopeRepositoryFake{valid: true, marked: true})
	scope.guard = fixture.guard
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("reserve synchronous work")
	}
	return fixture, stage, scope, reservation, input
}

type failingInitialLyricsCandidateSync struct {
	sourcefs.OutputDirectory
	sourcefs.OutputMutation
	sourcefs.OutputCleanup
	sourcefs.OutputDirectorySync
	err     error
	started chan struct{}
	resume  chan struct{}
}

func (directory failingInitialLyricsCandidateSync) SyncExistingRegular(context.Context, string, fs.FileInfo) error {
	if directory.started != nil {
		close(directory.started)
		<-directory.resume
	}
	return directory.err
}

func rebuiltInitialLyricsCandidate(t *testing.T, stage *preparedPublicationStage, scope *PublicationExecutionScope, reservation *publicationSyncReservation) (persistence.PublicationCandidateRebuildRecord, error) {
	t.Helper()
	input, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsRecorded)
	intent, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability); err != nil {
		t.Fatal(err)
	}
	resetErr := stage.resetInitialLyricsCandidate(context.Background(), scope, capability)
	if runtime.GOOS == "windows" {
		assertInitialLyricsResetUnsupported(t, stage, scope, reservation, capability, resetErr, input)
		return intent, resetErr
	}
	if resetErr != nil {
		t.Fatal(resetErr)
	}
	if err := os.WriteFile(stage.paths.OutputPath, []byte("transformed candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	return intent, nil
}

// assertInitialLyricsResetUnsupported pins the fail-closed Windows behavior:
// the directory flush is unsupported, so the reset must quarantine its scope,
// keep the prepared input, leave the exclusively created empty candidate, and
// consume the capability rather than claim a completed reset.
func assertInitialLyricsResetUnsupported(t *testing.T, stage *preparedPublicationStage, scope *PublicationExecutionScope, reservation *publicationSyncReservation, capability *initialLyricsRebuildCapability, err error, input []byte) {
	t.Helper()
	if !errors.Is(err, sourcefs.ErrUnsupported) {
		t.Fatalf("initial lyrics reset error = %v, want sourcefs.ErrUnsupported on Windows", err)
	}
	if capability.active {
		t.Fatal("unsupported reset did not consume its capability")
	}
	if !scope.unknown || scope.initialCopy || reservation.borrowed {
		t.Fatal("unsupported reset did not quarantine and release the operation fence")
	}
	if scope.reserveSynchronousWork() != nil {
		t.Fatal("quarantined scope admitted new synchronous work")
	}
	if _, err := scope.Run(context.Background(), "must-not-run-after-unsupported-reset"); err == nil {
		t.Fatal("quarantined scope admitted a command")
	}
	info, err := os.Stat(stage.paths.OutputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("candidate after unsupported reset = %v, err=%v, want existing empty regular file", info, err)
	}
	actual, err := os.ReadFile(stage.paths.InputPath)
	if err != nil || string(actual) != string(input) {
		t.Fatalf("prepared input changed after unsupported reset: %v", err)
	}
}

func attachInitialLyricsRebuildJournal(stage *preparedPublicationStage, outcome persistence.PublicationOptionalLyricsOutcome) *initialLyricsRebuildTestJournal {
	journal := &initialLyricsRebuildTestJournal{PublicationStagePreparationRepository: stage.repository, outcome: outcome}
	stage.repository = journal
	return journal
}

func TestInitialLyricsRebuildRequiresExactSynchronousReservation(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	if !stage.validInitialLyricsRebuildOwner(scope, reservation) {
		t.Fatal("exact active reservation was rejected")
	}
	borrowed := *reservation
	borrowed.borrowed = true
	if stage.validInitialLyricsRebuildOwner(scope, &borrowed) {
		t.Fatal("borrowed reservation was accepted")
	}
	other := &PublicationExecutionScope{guard: scope.guard}
	if stage.validInitialLyricsRebuildOwner(other, reservation) {
		t.Fatal("reservation from another scope was accepted")
	}
	stage.guard.DeliveryToken = "different"
	if stage.validInitialLyricsRebuildOwner(scope, reservation) {
		t.Fatal("scope with a different guard was accepted")
	}
}

func TestSameRebuildIdentityRequiresSizeMtimeAndDigest(t *testing.T) {
	size, mtime := int64(3), int64(4)
	a := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "digest"}
	b := clonePublicationIdentity(a)
	if !sameRebuildIdentity(a, b) {
		t.Fatal("identical evidence was rejected")
	}
	changedSize := int64(5)
	b.SizeBytes = &changedSize
	if sameRebuildIdentity(a, b) {
		t.Fatal("different size was accepted")
	}
	b = clonePublicationIdentity(a)
	b.SHA256 = "other"
	if sameRebuildIdentity(a, b) {
		t.Fatal("different digest was accepted")
	}
}

func TestInitialLyricsRebuildResetNativeLifecycle(t *testing.T) {
	_, stage, scope, reservation, input := initialLyricsRebuildFixture(t)
	attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsRecorded)
	_, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.initialCopy || !reservation.borrowed {
		t.Fatal("evidence preparation did not fence scope")
	}
	if scope.reserveSynchronousWork() != nil {
		t.Fatal("new reservation was admitted during evidence fence")
	}
	if _, err := scope.Run(context.Background(), "must-not-run"); err == nil {
		t.Fatal("command was admitted during evidence fence")
	}
	if err := stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability); err != nil {
		t.Fatal(err)
	}
	resetErr := stage.resetInitialLyricsCandidate(context.Background(), scope, capability)
	if runtime.GOOS == "windows" {
		assertInitialLyricsResetUnsupported(t, stage, scope, reservation, capability, resetErr, input)
		if err := stage.resetInitialLyricsCandidate(context.Background(), scope, capability); err == nil {
			t.Fatal("reused reset capability")
		}
		return
	}
	if resetErr != nil {
		t.Fatal(resetErr)
	}
	if scope.unknown || scope.initialCopy || reservation.borrowed {
		t.Fatal("successful reset did not release its known-success fence")
	}
	if _, err := scope.Run(context.Background(), "may-run-after-reset"); err != nil {
		t.Fatalf("scope remained command-fenced after successful reset: %v", err)
	}
	got, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reset candidate has %d bytes, want empty", len(got))
	}
	if actual, err := os.ReadFile(stage.paths.InputPath); err != nil || string(actual) != string(input) {
		t.Fatalf("prepared input changed: %v", err)
	}
	if err := stage.resetInitialLyricsCandidate(context.Background(), scope, capability); err == nil {
		t.Fatal("reused reset capability")
	}
}

func TestInitialLyricsRebuildRefusedAckDoesNotUnlink(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsAlreadyRecorded)
	before, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	_, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability); err == nil {
		t.Fatal("accepted non-new ACK")
	}
	after, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("candidate changed after refused ACK: %v", err)
	}
	if !scope.unknown {
		t.Fatal("ambiguous/refused ACK did not quarantine scope")
	}
}

func TestInitialLyricsRebuildCandidateSubstitutionIsRetained(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsRecorded)
	_, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stage.paths.OutputPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage.paths.OutputPath, []byte("substitute"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stage.resetInitialLyricsCandidate(context.Background(), scope, capability); err == nil {
		t.Fatal("accepted substituted candidate")
	}
	got, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil || string(got) != "substitute" {
		t.Fatalf("substitute was not retained: %q %v", got, err)
	}
}

func TestInitialLyricsRebuildIntentFenceBlocksUntilACK(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	journal := attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsRecorded)
	journal.started, journal.resume = make(chan struct{}), make(chan struct{})
	_, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability) }()
	<-journal.started
	if scope.reserveSynchronousWork() != nil {
		t.Fatal("reservation admitted while ACK was blocked")
	}
	if _, err := scope.Run(context.Background(), "must-not-run"); err == nil {
		t.Fatal("command admitted while ACK was blocked")
	}
	close(journal.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInitialLyricsRebuildCapabilityConcurrentConsumeIsSingleUse(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	attachInitialLyricsRebuildJournal(stage, persistence.PublicationOptionalLyricsRecorded)
	_, capability, err := stage.prepareInitialLyricsRebuild(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.acknowledgeInitialLyricsRebuildIntent(context.Background(), capability); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- stage.resetInitialLyricsCandidate(context.Background(), scope, capability)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	outcomes := make([]error, 0, 2)
	for result := range results {
		outcomes = append(outcomes, result)
	}
	if runtime.GOOS == "windows" {
		unsupported := 0
		for _, outcome := range outcomes {
			if errors.Is(outcome, sourcefs.ErrUnsupported) {
				unsupported++
			}
		}
		if unsupported != 1 {
			t.Fatalf("concurrent unsupported reset errors=%d, want exactly one", unsupported)
		}
		if !scope.unknown || scope.initialCopy || reservation.borrowed {
			t.Fatal("unsupported reset did not quarantine the scope")
		}
		data, err := os.ReadFile(stage.paths.OutputPath)
		if err != nil || len(data) != 0 {
			t.Fatalf("candidate after unsupported reset is not empty: %v", err)
		}
		return
	}
	successes := 0
	for _, outcome := range outcomes {
		if outcome == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent reset successes=%d, want exactly one", successes)
	}
	data, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil || len(data) != 0 {
		t.Fatalf("final candidate is not the successful empty reset: %v", err)
	}
}

func TestInitialLyricsRebuildFinishNativeSyncAndFence(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	intent, resetErr := rebuiltInitialLyricsCandidate(t, stage, scope, reservation)
	if runtime.GOOS == "windows" {
		if _, err := stage.finishInitialLyricsRebuild(context.Background(), scope, reservation, intent); err == nil {
			t.Fatalf("finish reported completion after unsupported reset: %v", resetErr)
		}
		return
	}
	block := failingInitialLyricsCandidateSync{OutputDirectory: stage.directory, OutputMutation: stage.directory.(sourcefs.OutputMutation), OutputCleanup: stage.directory.(sourcefs.OutputCleanup), err: nil, started: make(chan struct{}), resume: make(chan struct{})}
	if syncer, ok := stage.directory.(sourcefs.OutputDirectorySync); ok {
		block.OutputDirectorySync = syncer
	}
	stage.directory = block
	done := make(chan error, 1)
	go func() {
		_, err := stage.finishInitialLyricsRebuild(context.Background(), scope, reservation, intent)
		done <- err
	}()
	<-block.started
	if scope.reserveSynchronousWork() != nil {
		t.Fatal("reservation admitted during finish sync")
	}
	if _, err := scope.Run(context.Background(), "must-not-run"); err == nil {
		t.Fatal("command admitted during finish sync")
	}
	close(block.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if scope.unknown || scope.initialCopy || reservation.borrowed {
		t.Fatal("successful finish left scope fenced or quarantined")
	}
	if _, err := scope.Run(context.Background(), "may-run-after-finish"); err != nil {
		t.Fatalf("scope remained fenced after finish: %v", err)
	}
}

func TestInitialLyricsRebuildFinishSyncFailureQuarantines(t *testing.T) {
	_, stage, scope, reservation, _ := initialLyricsRebuildFixture(t)
	intent, resetErr := rebuiltInitialLyricsCandidate(t, stage, scope, reservation)
	if runtime.GOOS == "windows" {
		if _, err := stage.finishInitialLyricsRebuild(context.Background(), scope, reservation, intent); err == nil {
			t.Fatalf("finish reported completion after unsupported reset: %v", resetErr)
		}
		return
	}
	syncErr := errors.New("initial lyrics injected sync failure")
	failed := failingInitialLyricsCandidateSync{OutputDirectory: stage.directory, OutputMutation: stage.directory.(sourcefs.OutputMutation), OutputCleanup: stage.directory.(sourcefs.OutputCleanup), err: syncErr}
	if syncer, ok := stage.directory.(sourcefs.OutputDirectorySync); ok {
		failed.OutputDirectorySync = syncer
	}
	stage.directory = failed
	if _, err := stage.finishInitialLyricsRebuild(context.Background(), scope, reservation, intent); !errors.Is(err, syncErr) {
		t.Fatalf("finish sync error=%v, want injected failure", err)
	}
	if !scope.unknown || scope.initialCopy || reservation.borrowed {
		t.Fatal("failed sync did not quarantine and release the operation fence")
	}
}
