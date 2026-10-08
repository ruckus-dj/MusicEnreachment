//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestOutputAdmissionSessionPinsGateAndConnectionAcrossTransactions(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	repository := NewSetupManagerRepository(database)
	ctx := context.Background()
	entered := make(chan int)
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- repository.WithOutputAdmissionSession(ctx, func(session *OutputAdmissionSession) error {
			var sessionPID int
			if err := session.RunInTx(ctx, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&sessionPID) }); err != nil {
				return err
			}
			entered <- sessionPID
			<-release
			var secondPID int
			if err := session.RunInTx(ctx, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&secondPID) }); err != nil {
				return err
			}
			if secondPID != sessionPID {
				return fmt.Errorf("session backend changed from %d to %d", sessionPID, secondPID)
			}
			return nil
		})
	}()
	pid := <-entered
	if pid <= 0 {
		t.Fatalf("session backend pid = %d", pid)
	}
	exclusive := startCoordinationTransaction(t, database, func(ctx context.Context, tx bun.Tx) error {
		return lockOutputAdmissionGateExclusive(ctx, tx)
	})
	assertWaitingForPostgresLock(t, database, awaitCoordinationPID(t, exclusive.pid))
	close(release)
	if err := <-finished; err != nil {
		t.Fatalf("output admission session: %v", err)
	}
	assertTransactionSucceeded(t, exclusive.done)
}

func TestCleanupDeliverySessionSerializesSameOperation(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	repository := NewSetupManagerRepository(database)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	operationID := uuid.New()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- repository.withCleanupDeliverySession(ctx, operationID, func(*OutputAdmissionSession) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- repository.withCleanupDeliverySession(ctx, operationID, func(*OutputAdmissionSession) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second same-operation session entered while the first held its delivery lock")
	case err := <-secondDone:
		t.Fatalf("second session returned before the first released its lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-ctx.Done():
		t.Fatalf("second same-operation session did not enter after release: %v", ctx.Err())
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first cleanup delivery session: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second cleanup delivery session: %v", err)
	}
}

func TestCleanupJobArgumentsMustBeExactOperationIdentity(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	if err := validateCleanupJobArgs(cleanupTestJobArgs{OperationID: id}, id); err != nil {
		t.Fatalf("valid cleanup job args: %v", err)
	}
	for _, test := range []struct {
		name string
		args river.JobArgs
	}{
		{name: "wrong operation", args: cleanupTestJobArgs{OperationID: uuid.New()}},
		{name: "extra field", args: cleanupTestJobArgsWithExtra{OperationID: id, Extra: "unexpected"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCleanupJobArgs(test.args, id); err == nil {
				t.Fatal("unsafe cleanup job arguments were accepted")
			}
		})
	}
}

func TestCleanupStartupRecoveryFailsPersistedRunningAndQueuedOrphans(t *testing.T) {
	for _, initialState := range []string{"running", "queued"} {
		t.Run(initialState, func(t *testing.T) {
			t.Parallel()
			ctx, repository, artifactID, fence := admitCleanupOutcomeFixture(t)
			if initialState == "queued" {
				if _, err := repository.db.ExecContext(ctx, `UPDATE operation SET state='queued' WHERE id=?`, fence.OperationID); err != nil {
					t.Fatalf("make cleanup delivery queued: %v", err)
				}
			}
			if err := repository.RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(ctx, fence.OperationID, fence.OperationAttempt, fence.JobID); err != nil {
				t.Fatalf("recover orphaned %s cleanup: %v", initialState, err)
			}
			var state string
			if err := repository.db.NewRaw(`SELECT state FROM operation WHERE id=?`, fence.OperationID).Scan(ctx, &state); err != nil {
				t.Fatalf("read recovered cleanup state: %v", err)
			}
			if state != "failed" {
				t.Fatalf("recovered cleanup state = %q, want failed", state)
			}
			var artifactCount int
			if err := repository.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &artifactCount); err != nil {
				t.Fatalf("check artifact after recovery: %v", err)
			}
			if artifactCount != 1 {
				t.Fatalf("startup recovery changed filesystem-owned artifact record: %d rows", artifactCount)
			}
		})
	}
}

func TestCleanupRecoveryWaitsForActiveDeliveryClaim(t *testing.T) {
	t.Parallel()
	ctx, repository, artifactID, fence := admitCleanupOutcomeFixture(t)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	executorEntered := make(chan struct{})
	releaseExecutor := make(chan struct{})
	deliveryDone := make(chan error, 1)
	go func() {
		deliveryDone <- repository.RunSourceAnalysisArtifactCleanupDelivery(ctx, fence, func(context.Context, SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
			close(executorEntered)
			<-releaseExecutor
			return SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
		})
	}()
	<-executorEntered
	recoveryDone := make(chan error, 1)
	go func() {
		recoveryDone <- repository.RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(ctx, fence.OperationID, fence.OperationAttempt, fence.JobID)
	}()
	select {
	case err := <-recoveryDone:
		t.Fatalf("recovery returned while the executor held its claim: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseExecutor)
	if err := <-deliveryDone; err != nil {
		t.Fatalf("finish cleanup delivery: %v", err)
	}
	if err := <-recoveryDone; err == nil {
		t.Fatal("recovery unexpectedly cleared a claim after the delivery completed")
	}
	var operationState, itemState string
	if err := repository.db.NewRaw(`SELECT state FROM operation WHERE id=?`, fence.OperationID).Scan(ctx, &operationState); err != nil {
		t.Fatalf("read operation after serialized recovery: %v", err)
	}
	if err := repository.db.NewRaw(`SELECT state FROM source_analysis_artifact_cleanup_item WHERE artifact_id=?`, artifactID).Scan(ctx, &itemState); err != nil {
		t.Fatalf("read cleanup claim after serialized recovery: %v", err)
	}
	if operationState != "succeeded" || itemState != "succeeded" {
		t.Fatalf("delivery/recovery states = %q/%q, want succeeded/succeeded", operationState, itemState)
	}
}

type cleanupTestJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (cleanupTestJobArgs) Kind() string { return SourceAnalysisArtifactCleanupJobKind }

type cleanupTestJobArgsWithExtra struct {
	OperationID uuid.UUID `json:"operation_id"`
	Extra       string    `json:"extra"`
}

func (cleanupTestJobArgsWithExtra) Kind() string { return SourceAnalysisArtifactCleanupJobKind }

func TestStrictArtifactCleanupIDsRejectEmptyNilAndDuplicate(t *testing.T) {
	t.Parallel()
	if _, err := strictArtifactIDs(nil); err == nil {
		t.Fatal("empty artifact snapshot was accepted")
	}
	if _, err := strictArtifactIDs([]uuid.UUID{uuid.Nil}); err == nil {
		t.Fatal("zero UUID artifact was accepted")
	}
	id := uuid.New()
	if _, err := strictArtifactIDs([]uuid.UUID{id, id}); err == nil {
		t.Fatal("duplicate artifact identifiers were accepted")
	}
	ids, err := strictArtifactIDs([]uuid.UUID{uuid.New(), id})
	if err != nil || len(ids) != 2 || ids[0].String() > ids[1].String() {
		t.Fatalf("valid artifact identifiers = %v, %v", ids, err)
	}
}
