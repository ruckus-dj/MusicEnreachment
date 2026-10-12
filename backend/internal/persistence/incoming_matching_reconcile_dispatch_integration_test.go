//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type failAfterMatchingReconcileInsert struct{ persistence.RiverInserter }

func (client failAfterMatchingReconcileInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := client.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	return result, errors.New("injected dispatch failure")
}

type gatedMatchingReconcileInsert struct {
	persistence.RiverInserter
	inserted chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (client *gatedMatchingReconcileInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := client.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	client.once.Do(func() {
		close(client.inserted)
		<-client.release
	})
	return result, nil
}

func TestDispatchPendingIncomingMatchingReconciliationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/matching-reconcile-dispatch", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	operation := fixture.batchOperation(t)
	if err := fixture.admit(t, ctx, operation); err != nil {
		t.Fatalf("admit analysis: %v", err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE operation SET state='running' WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	digest[0] = 1
	variantID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO media_variant (id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES (?,?,?,?,?,?)`, variantID, fixture.work.SizeBytes, digest, time.Now().UTC(), "SHA-256", operation.ID); err != nil {
		t.Fatalf("insert SHA result: %v", err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='sha256'`, variantID, fixture.work.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}
	if err := fixture.repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "succeeded", "completed", ""); err != nil {
		t.Fatalf("settle analysis: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.database.NewSelect().Model(&event).Where("source_operation_id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatalf("read outbox event: %v", err)
	}
	client := openScanEnqueueRiver(t, fixture.database)
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, failAfterMatchingReconcileInsert{client}); err == nil {
		t.Fatal("expected injected dispatch failure")
	}
	storedEvent, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	event = *storedEvent
	if event.State != "pending" || event.RegisteredJobID != nil {
		t.Fatalf("failed dispatch was not rolled back: %+v", event)
	}
	var jobs int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("failed dispatch left %d River jobs", jobs)
	}
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch retry: %v", err)
	}
	storedEvent, err = fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	event = *storedEvent
	if event.State != "dispatched" || event.RegisteredJobID == nil || *event.RegisteredJobID < 1 {
		t.Fatalf("event not marked with job identity: %+v", event)
	}
	var args persistence.IncomingMatchingReconcileJobArgs
	var rawArgs string
	if err := fixture.database.NewRaw(`SELECT args FROM river_job WHERE id=?`, *event.RegisteredJobID).Scan(ctx, &rawArgs); err != nil {
		t.Fatalf("read River args: %v", err)
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("decode River args: %v", err)
	}
	if args.EventID != event.ID {
		t.Fatalf("job references event %s, want %s", args.EventID, event.ID)
	}
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("repeat dispatch: %v", err)
	}
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, persistence.IncomingMatchingReconcileJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("repeat dispatch created %d reconciliation jobs, want 1", jobs)
	}
}

func TestConcurrentMatchingReconcileDispatchCreatesOneReplacementPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/matching-reconcile-concurrent", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	client := openScanEnqueueRiver(t, fixture.database)
	event, err := fixture.repository.EnsureStartupIncomingMatchingReconciliation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || stored.RegisteredJobID == nil {
		t.Fatalf("read original registration: event=%+v err=%v", stored, err)
	}
	if _, err := fixture.database.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *stored.RegisteredJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	gated := &gatedMatchingReconcileInsert{RiverInserter: client, inserted: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gated.release) }) }
	defer release()
	firstDone := make(chan error, 1)
	go func() { firstDone <- fixture.repository.DispatchPendingMatchingReconciliations(ctx, gated) }()
	<-gated.inserted
	if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		release()
		t.Fatalf("concurrent dispatcher: %v", err)
	}
	release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first dispatcher: %v", err)
	}
	var count int
	if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'event_id'=?`, persistence.IncomingMatchingReconcileJobKind, event.ID).Scan(ctx, &count); err != nil || count != 2 {
		t.Fatalf("concurrent dispatch made %d total event jobs, want original plus one replacement: %v", count, err)
	}
}

func TestDispatchRecoversTerminalAndMissingMatchingReconcileJobsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newNormalizedWorkOperation(t, "/srv/matching-reconcile-recovery", true,
		[]persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}})
	client := openScanEnqueueRiver(t, fixture.database)
	for _, riverState := range []string{"cancelled", "discarded", "completed", "missing"} {
		t.Run(riverState, func(t *testing.T) {
			event, err := fixture.repository.EnsureStartupIncomingMatchingReconciliation(ctx)
			if err != nil {
				t.Fatalf("ensure startup event: %v", err)
			}
			if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("initial dispatch: %v", err)
			}
			original, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
			if err != nil || original.RegisteredJobID == nil {
				t.Fatalf("read initial delivery: event=%+v err=%v", original, err)
			}
			oldJobID := *original.RegisteredJobID
			if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("dispatch with active River delivery: %v", err)
			}
			unchanged, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
			if err != nil || unchanged.RegisteredJobID == nil || *unchanged.RegisteredJobID != oldJobID {
				t.Fatalf("active delivery was replaced: event=%+v err=%v", unchanged, err)
			}
			if _, claimed, err := fixture.repository.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, oldJobID, 1); err != nil || !claimed {
				t.Fatalf("claim old delivery: claimed=%v err=%v", claimed, err)
			}
			if riverState == "missing" {
				if _, err := fixture.database.NewRaw(`DELETE FROM river_job WHERE id=?`, oldJobID).Exec(ctx); err != nil {
					t.Fatalf("delete old River job: %v", err)
				}
			} else if _, err := fixture.database.NewRaw(`UPDATE river_job SET state=?,finalized_at=now() WHERE id=?`, riverState, oldJobID).Exec(ctx); err != nil {
				t.Fatalf("mark old River job %s: %v", riverState, err)
			}

			if riverState == "cancelled" {
				if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, failAfterMatchingReconcileInsert{client}); err == nil {
					t.Fatal("expected injected replacement failure")
				}
				stored, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
				if err != nil || stored.RegisteredJobID == nil || *stored.RegisteredJobID != oldJobID || stored.DeliveryEpoch != 1 || !stored.DeliveryClaimed {
					t.Fatalf("failed replacement changed event registration or claim: event=%+v err=%v", stored, err)
				}
				var count int
				if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'event_id'=?`, persistence.IncomingMatchingReconcileJobKind, event.ID).Scan(ctx, &count); err != nil || count != 1 {
					t.Fatalf("failed replacement left River job behind: count=%d err=%v", count, err)
				}
			}
			if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("replace terminal/missing delivery: %v", err)
			}
			replaced, err := fixture.repository.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
			if err != nil || replaced.RegisteredJobID == nil || *replaced.RegisteredJobID == oldJobID {
				t.Fatalf("event identity was not recovered in place: event=%+v err=%v", replaced, err)
			}
			if replaced.State != "dispatched" || replaced.DeliveryEpoch != 2 || replaced.HighestDeliveryAttempt != 0 || replaced.DeliveryClaimed || replaced.DispatchedAt == nil {
				t.Fatalf("replacement did not reset and fence the delivery: %+v", replaced)
			}
			startupEvent, err := fixture.repository.EnsureStartupIncomingMatchingReconciliation(ctx)
			if err != nil || startupEvent.ID != event.ID {
				t.Fatalf("startup recovery did not retain the open event identity: event=%+v err=%v", startupEvent, err)
			}
			if released, err := fixture.repository.ReleaseIncomingMatchingReconciliationClaim(ctx, event.ID, oldJobID, 1, 1); err != nil || released {
				t.Fatalf("old claim released after replacement: released=%v err=%v", released, err)
			}
			if completed, err := fixture.repository.MarkIncomingMatchingReconciliationComplete(ctx, event.ID, oldJobID, 1, 1); err != nil || completed {
				t.Fatalf("old claim completed after replacement: completed=%v err=%v", completed, err)
			}
			newJobID := *replaced.RegisteredJobID
			newEpoch, claimed, err := fixture.repository.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, newJobID, 1)
			if err != nil || !claimed || newEpoch <= replaced.DeliveryEpoch {
				t.Fatalf("new attempt 1 was not claimable with a newer epoch: epoch=%d claimed=%v err=%v", newEpoch, claimed, err)
			}
			if released, err := fixture.repository.ReleaseIncomingMatchingReconciliationClaim(ctx, event.ID, newJobID, 1, newEpoch); err != nil || !released {
				t.Fatalf("release new delivery claim: released=%v err=%v", released, err)
			}
			newEpoch, claimed, err = fixture.repository.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, newJobID, 1)
			if err != nil || !claimed {
				t.Fatalf("reclaim new attempt 1: epoch=%d claimed=%v err=%v", newEpoch, claimed, err)
			}
			if completed, err := fixture.repository.MarkIncomingMatchingReconciliationComplete(ctx, event.ID, newJobID, 1, newEpoch); err != nil || !completed {
				t.Fatalf("complete replacement delivery: completed=%v err=%v", completed, err)
			}
			if err := fixture.repository.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
				t.Fatalf("dispatch after done event: %v", err)
			}
			var count int
			wantJobs := 2
			if riverState == "missing" {
				wantJobs = 1
			}
			if err := fixture.database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'event_id'=?`, persistence.IncomingMatchingReconcileJobKind, event.ID).Scan(ctx, &count); err != nil || count != wantJobs {
				t.Fatalf("done event was redispatched: River job count=%d want=%d err=%v", count, wantJobs, err)
			}
		})
	}
}
