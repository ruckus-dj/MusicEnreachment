//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
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

func TestTerminalPublicationSettlementUsesCurrentPersistedRiverState(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repository := NewSetupManagerRepository(f.db)
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err := repository.ListTerminalPublicationCandidates(ctx, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("terminal candidates = %+v, %v", candidates, err)
	}
	if candidates[0].OperationID != f.guard.OperationID || candidates[0].RiverState != "cancelled" {
		t.Fatalf("terminal candidate identity = %+v", candidates[0])
	}
	settled, err := repository.FailTerminalPublication(ctx, candidates[0])
	if err != nil || !settled {
		t.Fatalf("terminal publication settlement = %v, %v", settled, err)
	}
	settled, err = repository.FailTerminalPublication(ctx, candidates[0])
	if err != nil || settled {
		t.Fatalf("repeated terminal publication settlement = %v, %v", settled, err)
	}
	var state, safeError string
	var finished bool
	if err := f.db.NewRaw(`SELECT state,safe_error,finished_at IS NOT NULL FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &safeError, &finished); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || safeError != failedPublicationDeliveryError || !finished || attempts != 1 {
		t.Fatalf("settled publication = state:%s error:%q finished:%v retained attempts:%d", state, safeError, finished, attempts)
	}
}

func TestTerminalPublicationSweepDoesNotFailLiveRiverDelivery(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repository := NewSetupManagerRepository(f.db)
	candidates, err := repository.ListTerminalPublicationCandidates(ctx, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("live delivery selected as terminal: %+v, %v", candidates, err)
	}
	changed, err := repository.FailTerminalPublication(ctx, TerminalPublicationCandidate{
		OperationID: f.guard.OperationID, ApplicationAttempt: f.guard.ApplicationAttempt,
		RiverJobID: f.guard.RiverJobID, RiverState: "discarded",
	})
	if err != nil || changed {
		t.Fatalf("live delivery failed by stale candidate: %v, %v", changed, err)
	}
}

func TestTerminalPublicationSweepSettlesDiscardedAndMissingJobs(t *testing.T) {
	for _, state := range []string{"discarded", "missing"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			f := newPublicationExecutionFixture(t)
			ctx := context.Background()
			repository := NewSetupManagerRepository(f.db)
			if state == "missing" {
				if _, err := f.db.ExecContext(ctx, `DELETE FROM river_job WHERE id=?`, f.guard.RiverJobID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='discarded',finalized_at=now() WHERE id=?`, f.guard.RiverJobID); err != nil {
				t.Fatal(err)
			}
			candidates, err := repository.ListTerminalPublicationCandidates(ctx, 10)
			if err != nil || len(candidates) != 1 || candidates[0].RiverState != state {
				t.Fatalf("terminal %s candidates = %+v, %v", state, candidates, err)
			}
			changed, err := repository.FailTerminalPublication(ctx, candidates[0])
			if err != nil || !changed {
				t.Fatalf("settle %s delivery = %v, %v", state, changed, err)
			}
		})
	}
}

func TestRetryFailedPublicationAtomicallyEnqueuesAndRetainsFrozenAttempt(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	pubSourceProtectionHoldWithFixture(t, f)
	ctx := context.Background()
	repository := NewSetupManagerRepository(f.db)
	var originalHold PublicationSourceHold
	if err := f.db.NewSelect().Model(&originalHold).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='discarded',finalized_at=now() WHERE id=?`, f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET state='failed',stage='failed',safe_error='fixture failure',finished_at=now() WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var frozen, manifest []byte
	var originalEpoch uuid.UUID
	var originalBytesCompleted, originalBytesTotal int64
	if err := f.db.NewRaw(`SELECT frozen_inputs,namespace_manifest,epoch FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &frozen, &manifest, &originalEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET bytes_completed=17,bytes_total=31 WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT bytes_completed,bytes_total FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &originalBytesCompleted, &originalBytesTotal); err != nil {
		t.Fatal(err)
	}
	client := openPublicationRetryRiver(t, ctx, f.db)
	operation, err := repository.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client)
	if err != nil {
		t.Fatalf("retry publication: %v", err)
	}
	if operation.State != "queued" || operation.Attempt != f.guard.ApplicationAttempt+1 || operation.RiverJobID == nil || *operation.RiverJobID == f.guard.RiverJobID {
		t.Fatalf("retried operation identity = %+v", operation)
	}
	var transferred PublicationSourceHold
	if err := f.db.NewSelect().Model(&transferred).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if transferred.ApplicationAttempt != operation.Attempt || transferred.RiverJobID != *operation.RiverJobID ||
		transferred.HoldUUID != originalHold.HoldUUID || transferred.SourceLocationID != originalHold.SourceLocationID ||
		transferred.WorkID != originalHold.WorkID || transferred.MediaVariantID != originalHold.MediaVariantID {
		t.Fatalf("retry changed source protection: before=%+v after=%+v", originalHold, transferred)
	}
	var state, queue string
	var args []byte
	if err := f.db.NewRaw(`SELECT state,queue,args FROM river_job WHERE id=?`, *operation.RiverJobID).Scan(ctx, &state, &queue, &args); err != nil {
		t.Fatal(err)
	}
	var jobArgs publication.TrackArgs
	if err := json.Unmarshal(args, &jobArgs); err != nil {
		t.Fatalf("decode new River job args %s: %v", args, err)
	}
	if state != "available" || queue != "publication" || jobArgs.OperationID != f.guard.OperationID {
		t.Fatalf("new River job = state:%s queue:%s args:%s", state, queue, args)
	}
	if operation.BytesCompleted != 0 || (operation.BytesTotal != nil && *operation.BytesTotal != 0) {
		t.Fatalf("retry did not reset progress: completed:%d total:%v (previous completed:%d total:%d)", operation.BytesCompleted, operation.BytesTotal, originalBytesCompleted, originalBytesTotal)
	}
	var retainedFrozen, retainedManifest []byte
	var retainedEpoch uuid.UUID
	if err := f.db.NewRaw(`SELECT frozen_inputs,namespace_manifest,epoch FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &retainedFrozen, &retainedManifest, &retainedEpoch); err != nil {
		t.Fatal(err)
	}
	if string(retainedFrozen) != string(frozen) || string(retainedManifest) != string(manifest) || retainedEpoch != originalEpoch {
		t.Fatal("publication retry changed frozen attempt inputs or epoch")
	}
	stale := TerminalPublicationCandidate{OperationID: f.guard.OperationID, ApplicationAttempt: f.guard.ApplicationAttempt, RiverJobID: f.guard.RiverJobID, RiverState: "discarded"}
	settled, err := repository.FailTerminalPublication(ctx, stale)
	if err != nil || settled {
		t.Fatalf("stale prior-job settlement = %v, %v", settled, err)
	}
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
