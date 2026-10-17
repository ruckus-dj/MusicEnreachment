package publication

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorSharedAndExclusiveAdmission(t *testing.T) {
	c := NewCoordinator()
	first, err := c.Acquire(context.Background(), []ResourceLock{{Key: "output", Mode: Shared}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Acquire(context.Background(), []ResourceLock{{Key: "output", Mode: Shared}})
	if err != nil {
		t.Fatal(err)
	}

	exclusive := make(chan func(), 1)
	go func() {
		release, acquireErr := c.Acquire(context.Background(), []ResourceLock{{Key: "output", Mode: Exclusive}})
		if acquireErr != nil {
			t.Error(acquireErr)
			return
		}
		exclusive <- release
	}()
	waitForWaiters(t, c, "output", 1)

	laterReader := make(chan func(), 1)
	go func() {
		release, acquireErr := c.Acquire(context.Background(), []ResourceLock{{Key: "output", Mode: Shared}})
		if acquireErr != nil {
			t.Error(acquireErr)
			return
		}
		laterReader <- release
	}()
	waitForWaiters(t, c, "output", 2)

	first()
	second()

	var releaseWriter func()
	select {
	case releaseWriter = <-exclusive:
	case <-time.After(testTimeout):
		t.Fatal("exclusive request did not acquire after readers released")
	}
	c.mu.Lock()
	state := c.locks["output"]
	writerHeld := state != nil && state.writer && len(state.waiters) == 1 && state.waiters[0].mode == Shared
	c.mu.Unlock()
	if !writerHeld {
		t.Fatal("later reader was not left queued while the writer held the resource")
	}
	select {
	case release := <-laterReader:
		release()
		t.Fatal("reader queued behind writer acquired while the writer was held")
	default:
	}

	releaseWriter()
	select {
	case release := <-laterReader:
		release()
	case <-time.After(testTimeout):
		t.Fatal("reader queued behind writer did not acquire")
	}
}

func TestCoordinatorAcquiresMultiResourceLocksInStableOrder(t *testing.T) {
	c := NewCoordinator()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, resources := range [][]ResourceLock{
		{{Key: "b", Mode: Exclusive}, {Key: "a", Mode: Exclusive}},
		{{Key: "a", Mode: Exclusive}, {Key: "b", Mode: Exclusive}},
	} {
		wg.Add(1)
		go func(resources []ResourceLock) {
			defer wg.Done()
			<-start
			release, err := c.Acquire(context.Background(), resources)
			if err != nil {
				t.Error(err)
				return
			}
			release()
		}(resources)
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("opposite-order requests deadlocked")
	}
}

func TestCoordinatorCancellationReleasesEarlierResources(t *testing.T) {
	c := NewCoordinator()
	blocker, err := c.Acquire(context.Background(), []ResourceLock{{Key: "b", Mode: Exclusive}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	acquired := make(chan error, 1)
	go func() {
		_, err := c.Acquire(ctx, []ResourceLock{{Key: "a", Mode: Exclusive}, {Key: "b", Mode: Exclusive}})
		acquired <- err
	}()
	waitForWaiters(t, c, "b", 1)
	cancel()
	select {
	case err := <-acquired:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire error = %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("canceled acquisition did not return")
	}
	probe, err := c.Acquire(context.Background(), []ResourceLock{{Key: "a", Mode: Exclusive}})
	if err != nil {
		t.Fatalf("earlier resource remained held after cancellation: %v", err)
	}
	probe()
	blocker()
}

func TestCoordinatorCancellationAfterAcquisitionDoesNotRelease(t *testing.T) {
	c := NewCoordinator()
	ctx, cancel := context.WithCancel(context.Background())
	release, err := c.Acquire(ctx, []ResourceLock{{Key: "key", Mode: Exclusive}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	defer waiterCancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Acquire(waiterCtx, []ResourceLock{{Key: "key", Mode: Exclusive}})
		done <- err
	}()
	waitForWaiters(t, c, "key", 1)
	waiterCancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting acquisition error = %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("canceled waiter did not return")
	}
	probeAcquired := make(chan func(), 1)
	go func() {
		probeRelease, err := c.Acquire(context.Background(), []ResourceLock{{Key: "key", Mode: Exclusive}})
		if err != nil {
			t.Error(err)
			return
		}
		probeAcquired <- probeRelease
	}()
	waitForWaiters(t, c, "key", 1)
	release()
	select {
	case probeRelease := <-probeAcquired:
		probeRelease()
	case <-time.After(testTimeout):
		t.Fatal("resource did not become available after explicit release")
	}
}

func TestCoordinatorGrantedCanceledWaiterReleasesGrantAndEarlierAcquisitions(t *testing.T) {
	c := NewCoordinator()
	blocker, err := c.Acquire(context.Background(), []ResourceLock{{Key: "b", Mode: Exclusive}})
	if err != nil {
		t.Fatal(err)
	}

	ctx := &deferredCancelContext{Context: context.Background()}
	canceled := make(chan error, 1)
	go func() {
		_, acquireErr := c.Acquire(ctx, []ResourceLock{
			{Key: "a", Mode: Exclusive},
			{Key: "b", Mode: Exclusive},
		})
		canceled <- acquireErr
	}()
	// Reaching a waiter on "b" means the earlier "a" acquisition already succeeded.
	waitForWaiters(t, c, "b", 1)

	later := make(chan func(), 1)
	go func() {
		release, acquireErr := c.Acquire(context.Background(), []ResourceLock{{Key: "b", Mode: Shared}})
		if acquireErr != nil {
			t.Error(acquireErr)
			return
		}
		later <- release
	}()
	waitForWaiters(t, c, "b", 2)

	// Cancel while the "b" waiter is blocked, then let dispatch grant it. Because
	// Done never closes, the waiter deterministically resumes through the ready
	// arm with ctx.Err() already set instead of racing a random select choice.
	ctx.cancel()
	blocker()

	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire error = %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("granted canceled acquisition did not return")
	}
	select {
	case release := <-later:
		release()
	case <-time.After(testTimeout):
		t.Fatal("later waiter did not proceed after the canceled grant was relinquished")
	}
	for _, key := range []string{"a", "b"} {
		if err := waitForNoState(t, c, key); err != nil {
			t.Fatalf("resource %q: %v", key, err)
		}
	}
}

func TestCoordinatorDuplicateModesAndIdempotentRelease(t *testing.T) {
	c := NewCoordinator()
	release, err := c.Acquire(context.Background(), []ResourceLock{
		{Key: "key", Mode: Shared},
		{Key: "key", Mode: Exclusive},
		{Key: "key", Mode: Shared},
	})
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		release func()
		err     error
	}, 1)
	go func() {
		waiterRelease, err := c.Acquire(waiting, []ResourceLock{{Key: "key", Mode: Shared}})
		done <- struct {
			release func()
			err     error
		}{waiterRelease, err}
	}()
	waitForWaiters(t, c, "key", 1)
	release()
	release()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("shared waiter failed after exclusive release: %v", result.err)
		}
		result.release()
	case <-time.After(testTimeout):
		t.Fatal("duplicate release failed to release exactly one exclusive lock")
	}
	cancel()
	if err := waitForNoState(t, c, "key"); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorRemovesUnusedStateWithoutBypassingWaiters(t *testing.T) {
	c := NewCoordinator()
	release, err := c.Acquire(context.Background(), []ResourceLock{{Key: "key", Mode: Exclusive}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan func(), 1)
	go func() {
		waiterRelease, err := c.Acquire(ctx, []ResourceLock{{Key: "key", Mode: Exclusive}})
		if err != nil {
			t.Error(err)
			return
		}
		done <- waiterRelease
	}()
	waitForWaiters(t, c, "key", 1)
	// The queued reference keeps this exact node registered while the holder releases.
	release()
	select {
	case waiterRelease := <-done:
		waiterRelease()
	case <-time.After(testTimeout):
		t.Fatal("queued waiter lost its state during holder release")
	}
	cancel()
	if err := waitForNoState(t, c, "key"); err != nil {
		t.Fatal(err)
	}
}

const testTimeout = 5 * time.Second

// deferredCancelContext reports cancellation through Err while leaving Done
// unclosed. A real canceled context also closes Done, which makes both select
// arms in acquireOne ready at once and lets Go choose between them at random.
// Keeping Done unclosed forces the granted-and-canceled cleanup path
// deterministically while still reporting the cancellation that path checks.
type deferredCancelContext struct {
	context.Context
	canceled atomic.Bool
}

func (ctx *deferredCancelContext) Done() <-chan struct{} { return nil }

func (ctx *deferredCancelContext) Err() error {
	if ctx.canceled.Load() {
		return context.Canceled
	}
	return nil
}

func (ctx *deferredCancelContext) cancel() { ctx.canceled.Store(true) }

func waitForWaiters(t *testing.T, c *Coordinator, key string, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for {
		c.mu.Lock()
		state := c.locks[key]
		ready := state != nil && len(state.waiters) == count
		c.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("resource %q did not reach %d queued waiters", key, count)
		default:
			runtime.Gosched()
		}
	}
}

func waitForNoState(t *testing.T, c *Coordinator, key string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for {
		c.mu.Lock()
		_, exists := c.locks[key]
		c.mu.Unlock()
		if !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("unused resource state was not removed")
		default:
			runtime.Gosched()
		}
	}
}
