package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationSourceCleanupFake struct {
	input          persistence.PublicationSourceCleanupDeliveryInput
	claim          persistence.PublicationDeliveryClaimOutcome
	start          bool
	startErr       error
	cancel         bool
	cancelErr      error
	removeResult   persistence.PublicationSourceCleanupRemovalOutcome
	removeErr      error
	settle         bool
	settleErr      error
	fail           bool
	failErr        error
	removeRequests []persistence.PublicationSourceCleanupRemovalRequest
	trace          []string
	guards         []persistence.PublicationSourceCleanupDeliveryGuard
	itemIDs        []uuid.UUID
	loadedID       uuid.UUID
}

func (fake *publicationSourceCleanupFake) LoadPublicationSourceCleanupDelivery(_ context.Context, id uuid.UUID) (persistence.PublicationSourceCleanupDeliveryInput, error) {
	fake.trace = append(fake.trace, "load")
	fake.loadedID = id
	fake.input.OperationID = id
	return fake.input, nil
}
func (fake *publicationSourceCleanupFake) recordGuard(g persistence.PublicationSourceCleanupDeliveryGuard) {
	fake.guards = append(fake.guards, g)
}
func (fake *publicationSourceCleanupFake) ClaimPublicationSourceCleanupDelivery(_ context.Context, g persistence.PublicationSourceCleanupDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
	fake.recordGuard(g)
	fake.trace = append(fake.trace, "claim")
	return fake.claim, nil
}
func (fake *publicationSourceCleanupFake) StartPublicationSourceCleanupExecution(_ context.Context, g persistence.PublicationSourceCleanupDeliveryGuard) (bool, error) {
	fake.recordGuard(g)
	fake.trace = append(fake.trace, "start")
	return fake.start, fake.startErr
}
func (fake *publicationSourceCleanupFake) CancelUnstartedPublicationSourceCleanupDelivery(_ context.Context, g persistence.PublicationSourceCleanupDeliveryGuard, safe string) (bool, error) {
	fake.recordGuard(g)
	fake.trace = append(fake.trace, "cancel:"+safe)
	return fake.cancel, fake.cancelErr
}
func (fake *publicationSourceCleanupFake) RemovePublicationSourceCleanupItem(ctx context.Context, g persistence.PublicationSourceCleanupDeliveryGuard, item uuid.UUID, remove func(context.Context, persistence.PublicationSourceCleanupRemovalRequest) error) (persistence.PublicationSourceCleanupRemovalOutcome, error) {
	fake.recordGuard(g)
	fake.itemIDs = append(fake.itemIDs, item)
	fake.trace = append(fake.trace, "remove")
	request := persistence.PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: `C:\Music`, RelativePath: `Artist\track.flac`, SizeBytes: 123, MTime: time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)}
	fake.removeRequests = append(fake.removeRequests, request)
	if fake.removeErr != nil {
		return fake.removeResult, fake.removeErr
	}
	if err := remove(ctx, request); err != nil {
		return fake.removeResult, err
	}
	return fake.removeResult, nil
}
func (fake *publicationSourceCleanupFake) SettlePublicationSourceCleanup(_ context.Context, g persistence.PublicationSourceCleanupDeliveryGuard) (bool, error) {
	fake.recordGuard(g)
	fake.trace = append(fake.trace, "settle")
	return fake.settle, fake.settleErr
}
func (fake *publicationSourceCleanupFake) FailPublicationSourceCleanupDelivery(_ context.Context, g persistence.PublicationSourceCleanupDeliveryGuard, safe string) (bool, error) {
	fake.recordGuard(g)
	fake.trace = append(fake.trace, "fail:"+safe)
	return fake.fail, fake.failErr
}

type publicationSourceCleanupFakeRemover struct {
	paths []persistence.PublicationSourceCleanupRemovalRequest
	err   error
}

func (remover *publicationSourceCleanupFakeRemover) Remove(_ context.Context, root, relative string, size int64, mtime time.Time) error {
	remover.paths = append(remover.paths, persistence.PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: root, RelativePath: relative, SizeBytes: size, MTime: mtime})
	return remover.err
}

func newPublicationSourceCleanupRunner(t *testing.T, fake *publicationSourceCleanupFake, remover *publicationSourceCleanupFakeRemover) *PublicationSourceCleanupRunner {
	t.Helper()
	runner, err := NewPublicationSourceCleanupRunner(fake, remover, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func validPublicationSourceCleanupFake() *publicationSourceCleanupFake {
	return &publicationSourceCleanupFake{input: persistence.PublicationSourceCleanupDeliveryInput{
		ApplicationAttempt: 2, Epoch: uuid.New(), PositionID: uuid.New(),
		ItemIDs: []uuid.UUID{uuid.New(), uuid.New()}, RemovalEnabled: true,
	}, claim: persistence.PublicationDeliveryAcquired, start: true,
		removeResult: persistence.PublicationSourceCleanupRemovalCompleted, settle: true}
}

func TestPublicationSourceCleanupRunnerCompletesExactItemsAndNativeRemoval(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	remover := new(publicationSourceCleanupFakeRemover)
	runner := newPublicationSourceCleanupRunner(t, fake, remover)
	opID := uuid.New()
	outcome, err := runner.ExecutePublicationSourceCleanupDelivery(context.Background(), opID, 91, 4)
	if err != nil || outcome != PublicationSourceCleanupCompleted {
		t.Fatalf("Execute() = %q, %v", outcome, err)
	}
	wantTrace := []string{"load", "claim", "start", "remove", "remove", "settle"}
	if !reflect.DeepEqual(fake.trace, wantTrace) || len(remover.paths) != 2 {
		t.Fatalf("trace/removals = %v / %v", fake.trace, remover.paths)
	}
	for index, request := range fake.removeRequests {
		if !reflect.DeepEqual(request, remover.paths[index]) {
			t.Fatalf("native remover received %v, persistence supplied %v", remover.paths[index], request)
		}
	}
	if fake.loadedID != opID || !reflect.DeepEqual(fake.itemIDs, fake.input.ItemIDs) {
		t.Fatalf("loader/item identities = %s / %v", fake.loadedID, fake.itemIDs)
	}
	guard := fake.guards[0]
	if guard.OperationID != opID || guard.ApplicationAttempt != fake.input.ApplicationAttempt || guard.RiverJobID != 91 || guard.RiverAttempt != 4 || guard.Epoch != fake.input.Epoch || guard.PositionID != fake.input.PositionID || guard.BootUUID != runner.bootUUID || guard.ClaimUUID == uuid.Nil || guard.DeliveryToken == "" {
		t.Fatalf("guard = %+v", guard)
	}
	for _, got := range fake.guards {
		if got != guard {
			t.Fatalf("guards differ: %+v != %+v", got, guard)
		}
	}
}

func TestPublicationSourceCleanupRunnerFreshGuardPerExecution(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	runner := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover))
	for i := 1; i <= 2; i++ {
		if _, err := runner.ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), int64(90+i), i); err != nil {
			t.Fatal(err)
		}
	}
	first, second := fake.guards[0], fake.guards[5]
	if first.BootUUID != second.BootUUID || first.ClaimUUID == second.ClaimUUID || first.DeliveryToken == second.DeliveryToken {
		t.Fatalf("guard identities were not refreshed: %+v / %+v", first, second)
	}
}

func TestPublicationSourceCleanupRunnerFalseTransitionsAndCancelErrors(t *testing.T) {
	t.Run("disabled cancellation false", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.input.RemovalEnabled = false
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if err != nil || outcome != PublicationSourceCleanupBlocked {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("disabled cancellation error", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.input.RemovalEnabled = false
		want := errors.New("cancel failed")
		fake.cancelErr = want
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || !errors.Is(err, want) {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("blocked start cancel false", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.start = false
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if err != nil || outcome != PublicationSourceCleanupBlocked {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("blocked start cancel error", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.start = false
		want := errors.New("cancel failed")
		fake.cancelErr = want
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || !errors.Is(err, want) {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("blocked item stops before settlement", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.removeResult = persistence.PublicationSourceCleanupRemovalBlocked
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || err != nil || !reflect.DeepEqual(fake.trace, []string{"load", "claim", "start", "remove", "fail:" + publicationSourceCleanupSafeError}) {
			t.Fatalf("= %q, %v, %v", outcome, err, fake.trace)
		}
	})
	t.Run("failure false is not recovery", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.removeResult = persistence.PublicationSourceCleanupRemovalBlocked
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || err != nil {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("settlement false is not completion", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		fake.settle = false
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || err != nil {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
	t.Run("settlement error preserves cause", func(t *testing.T) {
		fake := validPublicationSourceCleanupFake()
		want := errors.New("commit failed")
		fake.settleErr = want
		outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
		if outcome != PublicationSourceCleanupBlocked || !errors.Is(err, want) {
			t.Fatalf("= %q, %v", outcome, err)
		}
	})
}

func TestPublicationSourceCleanupRunnerNativeRemovalErrorFailsDelivery(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.removeResult = persistence.PublicationSourceCleanupRemovalBlocked
	fake.fail = true
	nativeErr := errors.New("native removal failed")
	remover := &publicationSourceCleanupFakeRemover{err: nativeErr}
	outcome, err := newPublicationSourceCleanupRunner(t, fake, remover).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || !errors.Is(err, nativeErr) {
		t.Fatalf("Execute() = %q, %v", outcome, err)
	}
	if !reflect.DeepEqual(fake.trace, []string{"load", "claim", "start", "remove", "fail:" + publicationSourceCleanupSafeError}) {
		t.Fatalf("trace = %v", fake.trace)
	}
}

func TestPublicationSourceCleanupRunnerDisabledCancelsWithoutClaim(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.input.RemovalEnabled = false
	fake.cancel = true
	outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if err != nil || outcome != PublicationSourceCleanupCancelled || !reflect.DeepEqual(fake.trace, []string{"load", "cancel:Publication source removal is disabled."}) {
		t.Fatalf("Execute() = %q, %v, trace %v", outcome, err, fake.trace)
	}
}

func TestPublicationSourceCleanupRunnerAlreadyOwnedDoesNotExecuteOrCancel(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.claim = persistence.PublicationDeliveryAlreadyOwned
	remover := new(publicationSourceCleanupFakeRemover)
	outcome, err := newPublicationSourceCleanupRunner(t, fake, remover).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if err != nil || outcome != PublicationSourceCleanupBlocked || !reflect.DeepEqual(fake.trace, []string{"load", "claim"}) {
		t.Fatalf("Execute() = %q, %v, trace %v", outcome, err, fake.trace)
	}
	if len(remover.paths) != 0 || len(fake.itemIDs) != 0 || len(fake.guards) != 1 {
		t.Fatalf("owned delivery performed work: paths=%v items=%v guards=%v", remover.paths, fake.itemIDs, fake.guards)
	}
}

func TestPublicationSourceCleanupRunnerBlockedStartAttemptsOnlySafeCancel(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.start = false
	fake.cancel = true
	outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if err != nil || outcome != PublicationSourceCleanupCancelled || !reflect.DeepEqual(fake.trace, []string{"load", "claim", "start", "cancel:Publication source removal is disabled."}) {
		t.Fatalf("Execute() = %q, %v, trace %v", outcome, err, fake.trace)
	}
}

func TestPublicationSourceCleanupRunnerRemovalErrorFailsWithoutSettlement(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.removeErr = errors.New("native removal failed")
	fake.fail = true
	outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || !errors.Is(err, fake.removeErr) || fake.trace[len(fake.trace)-1] != "fail:"+publicationSourceCleanupSafeError {
		t.Fatalf("Execute() = %q, %v, trace %v", outcome, err, fake.trace)
	}
	if fake.trace[len(fake.trace)-1] == "settle" {
		t.Fatalf("failure was incorrectly settled: %v", fake.trace)
	}
}

func TestPublicationSourceCleanupRunnerFailureWriteErrorDoesNotAcknowledge(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.removeErr = errors.New("native removal failed")
	fake.failErr = errors.New("database unavailable")
	outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if outcome != PublicationSourceCleanupBlocked || !errors.Is(err, fake.removeErr) || !errors.Is(err, fake.failErr) {
		t.Fatalf("Execute() = %q, %v", outcome, err)
	}
	if fake.trace[len(fake.trace)-1] == "settle" {
		t.Fatalf("failure-write error was acknowledged: %v", fake.trace)
	}
}

func TestPublicationSourceCleanupRunnerSettlementErrorIsNotCompleted(t *testing.T) {
	fake := validPublicationSourceCleanupFake()
	fake.settleErr = errors.New("settlement failed")
	outcome, err := newPublicationSourceCleanupRunner(t, fake, new(publicationSourceCleanupFakeRemover)).ExecutePublicationSourceCleanupDelivery(context.Background(), uuid.New(), 91, 1)
	if outcome != PublicationSourceCleanupBlocked || !errors.Is(err, fake.settleErr) {
		t.Fatalf("Execute() = %q, %v", outcome, err)
	}
}
