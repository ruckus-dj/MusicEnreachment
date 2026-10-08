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
	firstRelease, err := limiter.acquire(context.Background(), getLimit)
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
		release, err := limiter.acquire(context.Background(), getLimit)
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
	firstRelease, err := limiter.acquire(context.Background(), getLimit)
	if err != nil {
		t.Fatal(err)
	}
	secondRelease, err := limiter.acquire(context.Background(), getLimit)
	if err != nil {
		firstRelease()
		t.Fatal(err)
	}
	defer secondRelease()

	started := make(chan struct{})
	acquired := make(chan func(), 1)
	go func() {
		close(started)
		release, err := limiter.acquire(context.Background(), getLimit)
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
