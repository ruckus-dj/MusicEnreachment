//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestOutputAdmissionGateSharedAndExclusiveCoordination(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)

	readerRelease, releaseReader := coordinationRelease(t)
	readerAcquired := make(chan struct{})
	reader := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		close(readerAcquired)
		<-readerRelease
		return nil
	})
	awaitCoordinationPID(t, reader.pid)
	awaitCoordinationSignal(t, readerAcquired)

	// Admissions coexist, but an exclusive reset lock waits for every admission.
	if err := runCoordinationTransaction(database, func(ctx context.Context, tx bun.Tx) error {
		return AcquireOutputAdmissionGate(ctx, tx)
	}); err != nil {
		t.Fatalf("concurrent output admission: %v", err)
	}
	exclusive := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockOutputAdmissionGateExclusive(ctx, tx)
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, exclusive.pid))
	releaseReader()
	assertTransactionSucceeded(t, reader.done)
	assertTransactionSucceeded(t, exclusive.done)
}
