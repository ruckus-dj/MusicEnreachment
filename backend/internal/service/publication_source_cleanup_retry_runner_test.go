package service

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type explicitRetryFake struct {
	input          persistence.PublicationSourceCleanupExplicitRetryDeliveryInput
	claim          persistence.PublicationDeliveryClaimOutcome
	settled        bool
	settleErr      error
	fail           bool
	failErr        error
	resolution     persistence.PublicationSourceCleanupRetryResolutionOutcome
	resolveErr     error
	trace          []string
	guards         []persistence.PublicationSourceCleanupDeliveryGuard
	itemIDs        []uuid.UUID
	callbacks      int
	failContextErr error
	cancel         context.CancelFunc
	native         persistence.PublicationSourceCleanupNativeOutcome
	nativeErr      error
}

func (fake *explicitRetryFake) LoadPublicationSourceCleanupExplicitRetryDelivery(_ context.Context, id uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryDeliveryInput, error) {
	fake.trace = append(fake.trace, "load")
	fake.input.OperationID = id
	return fake.input, nil
}
func (fake *explicitRetryFake) ClaimPublicationSourceCleanupExplicitRetryDelivery(_ context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
	fake.trace = append(fake.trace, "claim")
	fake.guards = append(fake.guards, guard)
	return fake.claim, nil
}
func (fake *explicitRetryFake) ResolvePublicationSourceCleanupRetryItem(ctx context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard, item uuid.UUID, remove func(context.Context, persistence.PublicationSourceCleanupRemovalRequest) (persistence.PublicationSourceCleanupNativeOutcome, error)) (persistence.PublicationSourceCleanupRetryResolutionOutcome, error) {
	fake.trace = append(fake.trace, "resolve")
	fake.guards = append(fake.guards, guard)
	fake.itemIDs = append(fake.itemIDs, item)
	if remove != nil {
		fake.callbacks++
		native, err := remove(ctx, persistence.PublicationSourceCleanupRemovalRequest{ConfiguredRootPath: "/music", RelativePath: "track.flac", SizeBytes: 2, MTime: time.Unix(1, 0)})
		fake.native = native
		fake.nativeErr = err
		if err != nil {
			return persistence.PublicationSourceCleanupRetryResolutionBlocked, err
		}
	}
	if fake.cancel != nil {
		fake.cancel()
	}
	return fake.resolution, fake.resolveErr
}
func (fake *explicitRetryFake) SettlePublicationSourceCleanupExplicitRetry(_ context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard) (bool, error) {
	fake.trace = append(fake.trace, "settle")
	fake.guards = append(fake.guards, guard)
	return fake.settled, fake.settleErr
}
func (fake *explicitRetryFake) FailPublicationSourceCleanupExplicitRetryDelivery(ctx context.Context, guard persistence.PublicationSourceCleanupDeliveryGuard, _ string) (bool, error) {
	fake.trace = append(fake.trace, "fail")
	fake.guards = append(fake.guards, guard)
	fake.failContextErr = ctx.Err()
	return fake.fail, fake.failErr
}

type outcomeRetryFakeRemover struct {
	outcome sourcefs.SourceRemovalOutcome
	err     error
	calls   int
}

func (remover *outcomeRetryFakeRemover) RemoveWithOutcome(_ context.Context, _, _ string, _ int64, _ time.Time) (sourcefs.SourceRemovalOutcome, error) {
	remover.calls++
	return remover.outcome, remover.err
}

func newExplicitRetryRunner(t *testing.T, fake *explicitRetryFake, remover *outcomeRetryFakeRemover) *PublicationSourceCleanupExplicitRetryRunner {
	t.Helper()
	runner, err := NewPublicationSourceCleanupExplicitRetryRunner(fake, remover, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func validExplicitRetryFake() *explicitRetryFake {
	return &explicitRetryFake{input: persistence.PublicationSourceCleanupExplicitRetryDeliveryInput{ApplicationAttempt: 3, Epoch: uuid.New(), PositionID: uuid.New(), ItemIDs: []uuid.UUID{uuid.New(), uuid.New()}}, claim: persistence.PublicationDeliveryAcquired, resolution: persistence.PublicationSourceCleanupRetryResolutionCompleted, settled: true}
}

func TestExplicitRetryRunnerUsesFreshExactGuardAndSettles(t *testing.T) {
	fake := validExplicitRetryFake()
	remover := &outcomeRetryFakeRemover{outcome: sourcefs.SourceRemovalRemoved}
	runner := newExplicitRetryRunner(t, fake, remover)
	opID := uuid.New()
	outcome, err := runner.ExecutePublicationSourceCleanupExplicitRetry(context.Background(), opID, 912, 7)
	if err != nil || outcome != PublicationSourceCleanupCompleted {
		t.Fatalf("Execute() = %q, %v", outcome, err)
	}
	if !reflect.DeepEqual(fake.trace, []string{"load", "claim", "resolve", "resolve", "settle"}) || remover.calls != 2 {
		t.Fatalf("trace/remover calls = %v/%d", fake.trace, remover.calls)
	}
	guard := fake.guards[0]
	if guard.OperationID != opID || guard.ApplicationAttempt != 3 || guard.RiverJobID != 912 || guard.RiverAttempt != 7 || guard.Epoch != fake.input.Epoch || guard.PositionID != fake.input.PositionID || guard.BootUUID != runner.bootUUID || guard.ClaimUUID == uuid.Nil || guard.DeliveryToken == "" {
		t.Fatalf("guard = %+v", guard)
	}
	for _, got := range fake.guards {
		if got != guard {
			t.Fatalf("guard changed: %+v != %+v", got, guard)
		}
	}
}

func TestExplicitRetryRunnerBlockedClaimDoesNotFailOrTouchFilesystem(t *testing.T) {
	fake := validExplicitRetryFake()
	fake.claim = persistence.PublicationDeliveryAlreadyOwned
	remover := &outcomeRetryFakeRemover{}
	outcome, err := newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(context.Background(), uuid.New(), 9, 2)
	if outcome != PublicationSourceCleanupBlocked || err != nil || remover.calls != 0 || !reflect.DeepEqual(fake.trace, []string{"load", "claim"}) {
		t.Fatalf("= %q,%v trace=%v calls=%d", outcome, err, fake.trace, remover.calls)
	}
}

func TestExplicitRetryRunnerMappingErrorsAndStopsBeforeNextItem(t *testing.T) {
	for _, tc := range []struct {
		name      string
		native    sourcefs.SourceRemovalOutcome
		nativeErr error
	}{
		{name: "inconsistent removed", native: sourcefs.SourceRemovalRemoved, nativeErr: errors.New("remove failed")},
		{name: "unresolved missing", native: sourcefs.SourceRemovalMissingLeaf, nativeErr: errors.New("not absent")},
		{name: "unknown", native: sourcefs.SourceRemovalUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := validExplicitRetryFake()
			fake.resolution = persistence.PublicationSourceCleanupRetryResolutionBlocked
			fake.fail = true
			remover := &outcomeRetryFakeRemover{outcome: tc.native, err: tc.nativeErr}
			outcome, err := newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(context.Background(), uuid.New(), 9, 1)
			if outcome != PublicationSourceCleanupRecoveryRequired || err == nil || len(fake.itemIDs) != 1 || fake.callbacks != 1 || containsString(fake.trace, "settle") {
				t.Fatalf("= %q,%v trace=%v", outcome, err, fake.trace)
			}
			if tc.nativeErr != nil && !errors.Is(err, tc.nativeErr) {
				t.Fatalf("error did not preserve native cause: %v", err)
			}
		})
	}
}

func TestExplicitRetryRunnerMissingLeafOutcomeIsPassedWithExactError(t *testing.T) {
	fake := validExplicitRetryFake()
	remover := &outcomeRetryFakeRemover{outcome: sourcefs.SourceRemovalMissingLeaf, err: fs.ErrNotExist}
	// Persistence owns whether this observation is authorized; the runner only
	// translates the native classification and preserves the original error.
	fake.resolveErr = fs.ErrNotExist
	fake.resolution = persistence.PublicationSourceCleanupRetryResolutionBlocked
	fake.fail = true
	outcome, err := newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(context.Background(), uuid.New(), 9, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("= %q, %v", outcome, err)
	}
	if fake.native != persistence.PublicationSourceCleanupNativeMissingLeaf || !errors.Is(fake.nativeErr, fs.ErrNotExist) {
		t.Fatalf("native outcome/error = %q,%v", fake.native, fake.nativeErr)
	}
}

func TestExplicitRetryRunnerSettlementUncertainIsFailureAndBookkeptAfterCancellation(t *testing.T) {
	// Both settlement cases must reach the settle step: a successful native
	// removal is required, otherwise the resolver blocks before settling.
	settledTrace := []string{"load", "claim", "resolve", "resolve", "settle", "fail"}

	fake := validExplicitRetryFake()
	fake.settled = false
	fake.fail = true
	remover := &outcomeRetryFakeRemover{outcome: sourcefs.SourceRemovalRemoved}
	outcome, err := newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(context.Background(), uuid.New(), 9, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || err == nil {
		t.Fatalf("settlement false = %q,%v trace=%v", outcome, err, fake.trace)
	}
	if !containsString(fake.trace, "settle") || !reflect.DeepEqual(fake.trace, settledTrace) || remover.calls != 2 {
		t.Fatalf("settlement false trace/calls = %v/%d", fake.trace, remover.calls)
	}

	fake = validExplicitRetryFake()
	fake.settleErr = errors.New("settlement uncertain")
	fake.fail = true
	remover = &outcomeRetryFakeRemover{outcome: sourcefs.SourceRemovalRemoved}
	outcome, err = newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(context.Background(), uuid.New(), 9, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || !errors.Is(err, fake.settleErr) {
		t.Fatalf("settlement error = %q,%v", outcome, err)
	}
	if !containsString(fake.trace, "settle") || !reflect.DeepEqual(fake.trace, settledTrace) || remover.calls != 2 {
		t.Fatalf("settlement error trace/calls = %v/%d", fake.trace, remover.calls)
	}

	fake = validExplicitRetryFake()
	fake.fail = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A canceled call is rejected before acquisition; cancellation after acquiring
	// is covered by the fake resolver cancellation path below.
	if outcome, err := newExplicitRetryRunner(t, fake, new(outcomeRetryFakeRemover)).ExecutePublicationSourceCleanupExplicitRetry(ctx, uuid.New(), 9, 1); outcome != PublicationSourceCleanupBlocked || err == nil || len(fake.trace) != 0 {
		t.Fatalf("pre-canceled = %q,%v trace=%v", outcome, err, fake.trace)
	}

	fake = validExplicitRetryFake()
	fake.fail = true
	fake.resolveErr = errors.New("resolution returned")
	remover = &outcomeRetryFakeRemover{outcome: sourcefs.SourceRemovalRemoved}
	ctx, cancel = context.WithCancel(context.Background())
	fake.cancel = cancel
	outcome, err = newExplicitRetryRunner(t, fake, remover).ExecutePublicationSourceCleanupExplicitRetry(ctx, uuid.New(), 9, 1)
	if outcome != PublicationSourceCleanupRecoveryRequired || err == nil {
		t.Fatalf("post-acquisition cancellation = %q,%v trace=%v", outcome, err, fake.trace)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("resolver did not cancel the live context: %v", ctx.Err())
	}
	if !reflect.DeepEqual(fake.trace, []string{"load", "claim", "resolve", "fail"}) || remover.calls != 1 {
		t.Fatalf("post-acquisition cancellation trace/calls = %v/%d", fake.trace, remover.calls)
	}
	if fake.failContextErr != nil {
		t.Fatalf("failure bookkeeping received a canceled context: %v", fake.failContextErr)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
