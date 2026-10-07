//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

const coordinationWaitTimeout = 5 * time.Second

func TestToolsMoveGateWaitsForReadersAndRollbackReleases(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)

	readerRelease, releaseReader := coordinationRelease(t)
	readerAcquired := make(chan struct{})
	reader := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveReaders(ctx, tx); err != nil {
			return err
		}
		close(readerAcquired)
		<-readerRelease
		return nil
	})
	awaitCoordinationPID(t, reader.pid)
	awaitCoordinationSignal(t, readerAcquired)

	move := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockToolsMoveExclusive(ctx, tx)
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, move.pid))
	releaseReader()
	assertTransactionSucceeded(t, reader.done)
	assertTransactionSucceeded(t, move.done)

	// Rollback, not only commit, must release an exclusive gate.
	moveRelease, releaseMove := coordinationRelease(t)
	moveAcquired := make(chan struct{})
	moveHolder := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveExclusive(ctx, tx); err != nil {
			return err
		}
		close(moveAcquired)
		<-moveRelease
		return context.Canceled
	})
	awaitCoordinationPID(t, moveHolder.pid)
	awaitCoordinationSignal(t, moveAcquired)
	readerAttempt := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockToolsMoveReaders(ctx, tx)
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, readerAttempt.pid))
	releaseMove()
	if err := <-moveHolder.done; !errors.Is(err, context.Canceled) {
		t.Fatalf("exclusive holder transaction error = %v, want rollback cancellation", err)
	}
	assertTransactionSucceeded(t, readerAttempt.done)
}

func TestPackageSelectionReadersShareAndActivationWaits(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	readerRelease, releaseReader := coordinationRelease(t)
	readerAcquired := make(chan struct{})
	reader := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockPackageSelections(ctx, tx, []string{"fpcalc", "ffmpeg"}); err != nil {
			return err
		}
		close(readerAcquired)
		<-readerRelease
		return nil
	})
	awaitCoordinationPID(t, reader.pid)
	awaitCoordinationSignal(t, readerAcquired)

	// A second shared selector completes while the first still holds its locks.
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		return lockPackageSelections(ctx, tx, []string{"ffmpeg", "fpcalc"})
	}); err != nil {
		t.Fatalf("concurrent package selection: %v", err)
	}

	activation := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockPackageActivations(ctx, tx, []string{"fpcalc", "ffmpeg"})
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, activation.pid))
	releaseReader()
	assertTransactionSucceeded(t, reader.done)
	assertTransactionSucceeded(t, activation.done)
}

func TestSourceRootAndInstallationRowCoordination(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	rootRepository := NewSourceInventoryRepository(database)
	rootA := &SourceRoot{ID: uuid.New(), ConfiguredPath: "/source/a", DisplayName: "A", Enabled: true}
	rootB := &SourceRoot{ID: uuid.New(), ConfiguredPath: "/source/b", DisplayName: "B", Enabled: true}
	if err := rootRepository.CreateSourceRoot(ctx, rootA); err != nil {
		t.Fatalf("create root A: %v", err)
	}
	if err := rootRepository.CreateSourceRoot(ctx, rootB); err != nil {
		t.Fatalf("create root B: %v", err)
	}
	verifiedAt := time.Now().UTC()
	installation := &ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: "release-1", RelativePath: "ffmpeg/release-1", State: "ready",
		ExecutableVersions: json.RawMessage(`{"ffmpeg":"ffmpeg version 7.1","ffprobe":"ffprobe version 7.1"}`), ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
	if err := NewSetupManagerRepository(database).CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create installation: %v", err)
	}

	holderRelease, releaseHolder := coordinationRelease(t)
	holderAcquired := make(chan struct{})
	holder := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsRootsReaders(ctx, tx, []string{rootA.ConfiguredPath}); err != nil {
			return err
		}
		if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{installation.ID}); err != nil {
			return err
		}
		close(holderAcquired)
		<-holderRelease
		return nil
	})
	awaitCoordinationPID(t, holder.pid)
	awaitCoordinationSignal(t, holderAcquired)

	// A reader of the same root and installation, and a reader of another root
	// sharing that installation, both progress while the first reader is open.
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsRootsReaders(ctx, tx, []string{rootA.ConfiguredPath}); err != nil {
			return err
		}
		return lockCompatibleInstallations(ctx, tx, []uuid.UUID{installation.ID})
	}); err != nil {
		t.Fatalf("concurrent same-root reader: %v", err)
	}
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsRootsReaders(ctx, tx, []string{rootB.ConfiguredPath}); err != nil {
			return err
		}
		return lockCompatibleInstallations(ctx, tx, []uuid.UUID{installation.ID})
	}); err != nil {
		t.Fatalf("unrelated-root reader sharing installation: %v", err)
	}

	rootWriter := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockToolsRoots(ctx, tx, []string{rootA.ConfiguredPath}, false)
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, rootWriter.pid))
	installationWriter := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw("UPDATE tool_installation SET state = 'failed' WHERE id = ?", installation.ID).Exec(ctx)
		return err
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, installationWriter.pid))

	releaseHolder()
	assertTransactionSucceeded(t, holder.done)
	assertTransactionSucceeded(t, rootWriter.done)
	assertTransactionSucceeded(t, installationWriter.done)

	// The DELETE runs against its own holder transaction after the UPDATE has
	// drained. It can therefore only be blocked by the holder's shared
	// installation lock, never queued behind another writer on the same row.
	deleteRelease, releaseDeleteHolder := coordinationRelease(t)
	deleteAcquired := make(chan struct{})
	deleteHolder := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{installation.ID}); err != nil {
			return err
		}
		close(deleteAcquired)
		<-deleteRelease
		return nil
	})
	awaitCoordinationPID(t, deleteHolder.pid)
	awaitCoordinationSignal(t, deleteAcquired)
	installationDeleter := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw("DELETE FROM tool_installation WHERE id = ?", installation.ID).Exec(ctx)
		return err
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, installationDeleter.pid))

	releaseDeleteHolder()
	assertTransactionSucceeded(t, deleteHolder.done)
	assertTransactionSucceeded(t, installationDeleter.done)
}

func TestToolsRootsLockRequiresEveryRequestedRoot(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	rootRepository := NewSourceInventoryRepository(database)
	root := &SourceRoot{ID: uuid.New(), ConfiguredPath: "/source/registered", DisplayName: "Registered", Enabled: true}
	if err := rootRepository.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create root: %v", err)
	}

	// A single unregistered root must fail instead of locking nothing and
	// reporting success to a caller that is about to write.
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		return lockToolsRootsReaders(ctx, tx, []string{"/source/unregistered"})
	}); err == nil {
		t.Fatal("locking an unregistered root succeeded")
	}

	// A partially registered set must fail instead of pretending the whole set
	// is locked while one path silently matches no row.
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		return lockToolsRoots(ctx, tx, []string{root.ConfiguredPath, "/source/unregistered"}, false)
	}); err == nil {
		t.Fatal("locking a partially registered root set succeeded")
	}
}

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
