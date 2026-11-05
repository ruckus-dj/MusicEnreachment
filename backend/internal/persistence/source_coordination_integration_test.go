//go:build integration

package persistence

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

const coordinationWaitTimeout = 5 * time.Second

type coordinationAttempt struct {
	pid  <-chan int
	done <-chan error
}

func coordinationRelease(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	return release, closeRelease
}

func awaitCoordinationPID(t *testing.T, pid <-chan int) int {
	t.Helper()
	select {
	case value := <-pid:
		return value
	case <-time.After(coordinationWaitTimeout):
		t.Fatal("coordination transaction did not report its PostgreSQL PID")
		return 0
	}
}

func awaitCoordinationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(coordinationWaitTimeout):
		t.Fatal("coordination transaction did not acquire its lock")
	}
}

func startCoordinationTransaction(t *testing.T, database *bun.DB, run func(context.Context, bun.Tx) error) coordinationAttempt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), coordinationWaitTimeout*2)
	t.Cleanup(cancel)
	pid := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		done <- database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var backendPID int
			if err := tx.NewRaw("SELECT pg_backend_pid()").Scan(ctx, &backendPID); err != nil {
				return err
			}
			pid <- backendPID
			return run(ctx, tx)
		})
	}()
	return coordinationAttempt{pid: pid, done: done}
}

func runCoordinationTransaction(database *bun.DB, run func(context.Context, bun.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), coordinationWaitTimeout)
	defer cancel()
	return database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return run(ctx, tx) })
}

func assertWaitingForPostgresLock(t *testing.T, database *bun.DB, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), coordinationWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE pid = ? AND wait_event_type = 'Lock'
		)`, pid).Scan(ctx, &waiting)
		if err != nil {
			t.Fatalf("inspect PostgreSQL lock wait for pid %d: %v", pid, err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("PostgreSQL backend %d did not enter a lock wait", pid)
		case <-ticker.C:
		}
	}
}

func assertTransactionSucceeded(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("coordination transaction: %v", err)
		}
	case <-time.After(coordinationWaitTimeout):
		t.Fatal("coordination transaction did not complete")
	}
}
