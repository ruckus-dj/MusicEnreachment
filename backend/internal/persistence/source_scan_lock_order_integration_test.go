//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceScanRetryLocksRootBeforeOperationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/scan-lock-order")
	failed := failedSourceScanRetryOperation(t, ctx, repository, inventory, root, "traversing", "The traversal failed.")

	// This transaction takes the first lock in source-scan apply/admission order,
	// then explicitly requests the operation row just as a concurrent applier
	// would. The retry must be waiting on the root without already holding the
	// operation row, otherwise PostgreSQL detects the inverse-order deadlock.
	blocker, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `SELECT id FROM source_root WHERE id = ? FOR UPDATE`, root.ID); err != nil {
		t.Fatalf("lock source root: %v", err)
	}
	var blockerPID int
	if err := blocker.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read root-lock transaction pid: %v", err)
	}

	retryResult := make(chan error, 1)
	go func() {
		_, err := repository.RetrySourceScanOperationAndEnqueue(ctx, failed.ID, client,
			service.ScanSourceJobArgs{OperationID: failed.ID}, nil)
		retryResult <- err
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := database.NewRaw(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity AS waiter
				CROSS JOIN LATERAL unnest(pg_blocking_pids(waiter.pid)) AS blocker(pid)
				WHERE waiter.wait_event_type = 'Lock'
					AND waiter.query ILIKE '%source_root%FOR UPDATE%'
					AND blocker.pid = ?
			)`, blockerPID).Scan(ctx, &waiting); err != nil {
			t.Fatalf("observe the retry waiting on the root lock: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not block on the root row")
		}
		select {
		case err := <-retryResult:
			t.Fatalf("retry finished before the root lock was released: %v", err)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	var lockedOperation uuid.UUID
	if err := blocker.QueryRowContext(ctx, `SELECT id FROM operation WHERE id = ? FOR UPDATE`, failed.ID).Scan(&lockedOperation); err != nil {
		t.Fatalf("lock operation after root: %v", err)
	}
	if lockedOperation != failed.ID {
		t.Fatalf("locked operation = %s, want %s", lockedOperation, failed.ID)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("commit root-first transaction: %v", err)
	}

	select {
	case err := <-retryResult:
		if err != nil {
			t.Fatalf("retry after releasing the root-first blocker: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("retry did not finish after releasing the root lock: %v", ctx.Err())
	}
}
