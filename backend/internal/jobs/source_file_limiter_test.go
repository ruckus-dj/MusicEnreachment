package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceFileLimiterIsSharedAcrossSingletonDeliveries(t *testing.T) {
	limiter := new(sourceFileLimiter)
	getLimit := func(context.Context) (int, error) { return 1, nil }
	limiter.readLimit = getLimit
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire the first file slot: %v", err)
	}
	var releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(firstRelease) }
	defer releaseFirst()

	started := make(chan struct{})
	acquired := make(chan func(), 1)
	go func() {
		close(started)
		release, err := limiter.acquire(context.Background())
		if err == nil {
			acquired <- release
		}
	}()
	<-started

	select {
	case release := <-acquired:
		release()
		t.Fatal("a second singleton delivery passed the shared one-file limit")
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("the waiting singleton delivery did not acquire the released file slot")
	}
}

func TestSourceFileLimiterRechecksShrunkLimitAfterWake(t *testing.T) {
	limiter := new(sourceFileLimiter)
	var limit atomic.Int32
	limit.Store(2)
	getLimit := func(context.Context) (int, error) { return int(limit.Load()), nil }
	limiter.readLimit = getLimit
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondRelease, err := limiter.acquire(context.Background())
	if err != nil {
		firstRelease()
		t.Fatal(err)
	}
	defer secondRelease()

	started := make(chan struct{})
	acquired := make(chan func(), 1)
	go func() {
		close(started)
		release, err := limiter.acquire(context.Background())
		if err == nil {
			acquired <- release
		}
	}()
	<-started
	select {
	case release := <-acquired:
		release()
		t.Fatal("waiter acquired while both slots were occupied")
	case <-time.After(20 * time.Millisecond):
	}

	limit.Store(1)
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRelease()
	select {
	case release := <-acquired:
		release()
		t.Fatal("waiter used the old limit after it was reduced")
	case <-time.After(20 * time.Millisecond):
	}

	secondRelease()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after all active files were released")
	}
}

func TestSourceFileLimiterWakesWaiterWhenLimitIncreases(t *testing.T) {
	limiter := new(sourceFileLimiter)
	var limit atomic.Int32
	limit.Store(1)
	limiter.readLimit = func(context.Context) (int, error) { return int(limit.Load()), nil }
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer firstRelease()

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := limiter.acquire(context.Background())
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("waiter acquired before the limit increased")
	case <-time.After(20 * time.Millisecond):
	}

	limit.Store(2)
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("limit increase did not wake a blocked waiter")
	}
}

func TestSourceFileLimiterUsesPositivePersistedLimitWithoutCeiling(t *testing.T) {
	limiter := new(sourceFileLimiter)
	limiter.readLimit = func(context.Context) (int, error) { return 1001, nil }
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := limiter.limitSnapshot(); got != 1001 {
		t.Fatalf("limit = %d, want persisted value 1001", got)
	}

	limiter.readLimit = func(context.Context) (int, error) { return 0, nil }
	if err := limiter.Refresh(context.Background()); err == nil {
		t.Fatal("zero persisted limit should be rejected")
	}
	if got := limiter.limitSnapshot(); got != 1001 {
		t.Fatalf("invalid refresh changed the last valid limit to %d", got)
	}
}

func TestSourceFileLimiterTryAcquireDenialDoesNotChangeActiveCount(t *testing.T) {
	limiter := new(sourceFileLimiter)
	limiter.limit = 1
	release, ok := limiter.tryAcquire()
	if !ok {
		t.Fatal("first reservation was denied")
	}
	if _, ok := limiter.tryAcquire(); ok {
		t.Fatal("reservation passed a saturated singleton limit")
	}
	if limiter.active != 1 {
		t.Fatalf("active count after denied reservation = %d, want 1", limiter.active)
	}
	release()
}

func TestSourceFileLimiterCancellationAndReleaseAreSafe(t *testing.T) {
	limiter := new(sourceFileLimiter)
	if release, ok := limiter.tryAcquire(); !ok {
		t.Fatal("default limit should admit a reservation")
	} else {
		release()
		release()
	}

	limiter.readLimit = func(context.Context) (int, error) { return 1, nil }
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	occupied, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.acquire(ctx); err == nil {
		t.Fatal("acquire with a canceled context succeeded")
	}
	occupied()
	if release, ok := limiter.tryAcquire(); !ok {
		t.Fatal("canceled waiter leaked an admission reservation")
	} else {
		release()
	}
}

func TestSourceAnalysisGroupsScheduleAdditionalWorkAfterLimitIncrease(t *testing.T) {
	limiter := new(sourceFileLimiter)
	var limit atomic.Int32
	limit.Store(1)
	limiter.readLimit = func(context.Context) (int, error) { return int(limit.Load()), nil }
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	reservation, ok := limiter.tryAcquire()
	if !ok {
		t.Fatal("initial group reservation denied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan int, 2)
	releaseFirst := make(chan struct{})
	done := make(chan []error, 1)
	go func() {
		errs, _ := runSourceAnalysisGroups(ctx, limiter, reservation, 2, func(ctx context.Context, index int) error {
			started <- index
			if index == 0 {
				select {
				case <-releaseFirst:
				case <-ctx.Done():
				}
			}
			return nil
		})
		done <- errs
	}()
	awaitGroupStart(t, started, 0)
	limit.Store(2)
	if err := limiter.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	awaitGroupStart(t, started, 1)
	close(releaseFirst)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("groups did not finish")
	}
}

func TestSourceAnalysisGroupsRespectShrunkLimitForPendingWork(t *testing.T) {
	limiter := new(sourceFileLimiter)
	var limit atomic.Int32
	limit.Store(2)
	limiter.readLimit = func(context.Context) (int, error) { return int(limit.Load()), nil }
	if err := limiter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	reservation, ok := limiter.tryAcquire()
	if !ok {
		t.Fatal("initial group reservation denied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan int, 3)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	done := make(chan []error, 1)
	go func() {
		errs, _ := runSourceAnalysisGroups(ctx, limiter, reservation, 3, func(ctx context.Context, index int) error {
			started <- index
			select {
			case <-releases[index]:
			case <-ctx.Done():
			}
			return nil
		})
		done <- errs
	}()
	awaitGroupStarts(t, started, 0, 1)
	limit.Store(1)
	if err := limiter.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	close(releases[0])
	select {
	case index := <-started:
		t.Fatalf("pending group %d started while active groups were at the shrunken limit", index)
	case <-time.After(30 * time.Millisecond):
	}
	close(releases[1])
	awaitGroupStart(t, started, 2)
	close(releases[2])
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("groups did not finish")
	}
}

func awaitGroupStart(t *testing.T, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("started group %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("group %d did not start", want)
	}
}

// awaitGroupStarts collects a set of group starts whose relative order is
// scheduler-dependent, because groups admitted at a limit above one start
// concurrently.
func awaitGroupStarts(t *testing.T, started <-chan int, want ...int) {
	t.Helper()
	remaining := make(map[int]struct{}, len(want))
	for _, index := range want {
		remaining[index] = struct{}{}
	}
	for len(remaining) > 0 {
		select {
		case got := <-started:
			if _, ok := remaining[got]; !ok {
				t.Fatalf("started group %d, want one of %v", got, want)
			}
			delete(remaining, got)
		case <-time.After(time.Second):
			t.Fatalf("groups %v did not all start", want)
		}
	}
}
