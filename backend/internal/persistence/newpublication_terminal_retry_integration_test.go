//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"
)

// openPublicationRetryRiver returns an insert-only client backed by the real
// river_job table, so retry admission is proven against actual River inserts.
func openPublicationRetryRiver(t *testing.T, ctx context.Context, database *bun.DB) *river.Client[*sql.Tx] {
	t.Helper()
	initializeRiver(t, ctx, database)
	client, err := river.NewClient(riverdatabasesql.New(database.DB), &river.Config{})
	if err != nil {
		t.Fatalf("create publish retry River client: %v", err)
	}
	return client
}

type insertThenFailPublicationRiverInserter struct{ RiverInserter }

func (inserter insertThenFailPublicationRiverInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if _, err := inserter.RiverInserter.InsertTx(ctx, tx, args, opts); err != nil {
		return nil, err
	}
	return nil, errors.New("injected post-insert failure")
}

func TestRetryFailedPublicationRollsBackInsertedRiverJob(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	pubSourceProtectionHoldWithFixture(t, f)
	ctx := context.Background()
	repository := NewSetupManagerRepository(f.db)
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET state='failed',stage='failed',safe_error='fixture failure',finished_at=now() WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	client := openPublicationRetryRiver(t, ctx, f.db)
	_, err := repository.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, insertThenFailPublicationRiverInserter{RiverInserter: client})
	if err == nil {
		t.Fatal("retry unexpectedly succeeded")
	}
	var state string
	var attempt int
	if err := f.db.NewRaw(`SELECT state,attempt FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &attempt); err != nil {
		t.Fatal(err)
	}
	var newJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1' AND args->>'operation_id'=?`, f.guard.OperationID.String()).Scan(ctx, &newJobs); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || attempt != f.guard.ApplicationAttempt || newJobs != 0 {
		t.Fatalf("failed enqueue transaction left operation:%s attempt:%d new jobs:%d", state, attempt, newJobs)
	}
	var retained PublicationSourceHold
	if err := f.db.NewSelect().Model(&retained).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if retained.ApplicationAttempt != f.guard.ApplicationAttempt || retained.RiverJobID != f.guard.RiverJobID {
		t.Fatalf("failed retry lost source protection: %+v", retained)
	}
}

func TestConcurrentRetryFailedPublicationHasOneWinner(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repository := NewSetupManagerRepository(f.db)
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET state='failed',stage='failed',safe_error='fixture failure',finished_at=now() WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	client := openPublicationRetryRiver(t, ctx, f.db)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			<-start
			_, err := repository.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, failed := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	var enqueued int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1' AND args->>'operation_id'=?`, f.guard.OperationID.String()).Scan(ctx, &enqueued); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || failed != 1 || enqueued != 1 {
		t.Fatalf("concurrent retry outcomes = successes:%d failures:%d jobs:%d", succeeded, failed, enqueued)
	}
}
