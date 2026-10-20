//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type reconciliationDeliveryInserter struct {
	fail bool
	ids  []int64
	args []PublicationReconciliationJobArgs
}

func TestPublicationReconciliationDeliveryAttemptAndRepairFencesPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, []string{"scope"})
	}); err != nil {
		t.Fatal(err)
	}
	failed := &reconciliationDeliveryInserter{fail: true}
	if _, err := repo.DispatchPendingPublicationReconciliations(ctx, failed); err == nil {
		t.Fatal("enqueue failure accepted")
	}
	var n int
	if err := repo.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &n); err != nil || n != 0 {
		t.Fatalf("failed enqueue left job: %d %v", n, err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE state='pending' AND registered_job_id IS NULL`).Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("failed enqueue changed event: %d %v", n, err)
	}
	inserter := new(reconciliationDeliveryInserter)
	dispatch := func() (uuid.UUID, int64) {
		t.Helper()
		count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter)
		if err != nil || count != 1 {
			t.Fatalf("dispatch=%d err=%v", count, err)
		}
		a := inserter.args[len(inserter.args)-1]
		return a.OperationID, inserter.ids[len(inserter.ids)-1]
	}
	opID, jobID := dispatch()
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	old, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET attempt=2 WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	newGuard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 2)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("reclaim=%s err=%v", result, err)
	}
	if released, err := repo.ReleasePublicationReconciliationDelivery(ctx, old); err != nil || released {
		t.Fatalf("old release=%v err=%v", released, err)
	}
	ack := func(g PublicationReconciliationDeliveryGuard) bool {
		t.Helper()
		accepted := false
		err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var e error
			accepted, e = repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, g)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return accepted
	}
	if ack(old) {
		t.Fatal("old attempt guard acknowledged")
	}
	// The real River row no longer matches the latest successful claim.
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET attempt=3 WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if ack(newGuard) {
		t.Fatal("ack accepted after River attempt changed")
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='failed',safe_error='fixture',finished_at=now() WHERE id=?`, opID); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, []string{"repair"})
	}); err != nil {
		t.Fatal(err)
	}
	newOp, newJob := dispatch()
	var eventEpoch int64
	if err := repo.db.NewRaw(`SELECT delivery_epoch FROM publication_reconciliation_event WHERE operation_id=?`, newOp).Scan(ctx, &eventEpoch); err != nil || eventEpoch != 2 {
		t.Fatalf("repaired event epoch=%d err=%v", eventEpoch, err)
	}
	if newOp != opID || newJob == jobID {
		t.Fatalf("repair identity op=%s job=%d previous=%s/%d", newOp, newJob, opID, jobID)
	}
	// Old delivery identity cannot affect the repaired dispatched event.
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, newJob); err != nil {
		t.Fatal(err)
	}
	if ack(newGuard) {
		t.Fatal("old guard mutated repaired event")
	}
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, uuid.New(), []uuid.UUID{position}, []string{"bad"})
	}); err == nil {
		t.Fatal("wrong output epoch accepted")
	}
}

func TestPublicationReconciliationDispatchRepairsMissingRiverJobPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"preserve-me"})
	inserter := new(reconciliationDeliveryInserter)
	dispatched, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || dispatched != 1 {
		t.Fatalf("initial dispatch=%d err=%v", dispatched, err)
	}
	opID, oldJobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, oldJobID); err != nil {
		t.Fatal(err)
	}
	oldGuard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, oldJobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("old delivery claim=%s err=%v", result, err)
	}
	// Clear the non-FK operation reference before deleting the actual River row.
	if _, err := repo.db.ExecContext(ctx, `UPDATE operation SET river_job_id=NULL WHERE id=?`, opID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM river_job WHERE id=?`, oldJobID); err != nil {
		t.Fatalf("delete registered River job (schema FK prevents missing-job fixture): %v", err)
	}
	dispatched, err = repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || dispatched != 1 {
		t.Fatalf("repair dispatch=%d err=%v", dispatched, err)
	}
	newOpID, newJobID := inserter.args[len(inserter.args)-1].OperationID, inserter.ids[len(inserter.ids)-1]
	var eventEpoch int64
	if err := repo.db.NewRaw(`SELECT delivery_epoch FROM publication_reconciliation_event WHERE operation_id=?`, newOpID).Scan(ctx, &eventEpoch); err != nil || eventEpoch != 2 {
		t.Fatalf("repaired delivery epoch=%d err=%v", eventEpoch, err)
	}
	if newOpID != opID || newJobID == oldJobID {
		t.Fatalf("repair identity op=%s job=%d, wanted same operation %s and new job after %d", newOpID, newJobID, opID, oldJobID)
	}
	if _, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, oldJobID, 1); err != nil || result != PublicationReconciliationStale {
		t.Fatalf("deleted job claim=%s err=%v", result, err)
	}
	accepted := false
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		accepted, err = repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, oldGuard)
		return err
	}); err != nil || accepted {
		t.Fatalf("stale guard acknowledgement=%v err=%v", accepted, err)
	}
	var retained int
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND scope='preserve-me' AND generation=1`, epoch, position).Scan(ctx, &retained); err != nil || retained != 1 {
		t.Fatalf("desired dirty scope lost: count=%d err=%v", retained, err)
	}
}

func (i *reconciliationDeliveryInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	a, ok := args.(PublicationReconciliationJobArgs)
	if !ok {
		return nil, errors.New("unexpected job args")
	}
	i.args = append(i.args, a)
	if i.fail {
		return nil, errors.New("injected enqueue failure")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), encoded).Scan(&id); err != nil {
		return nil, err
	}
	i.ids = append(i.ids, id)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func reconciliationDeliveryFixture(t *testing.T) (*SetupManagerRepository, context.Context, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	position, release, track := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'reconciliation')`, release); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, track); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, position, release, track); err != nil {
		t.Fatal(err)
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSetupManagerRepository(db), ctx, epoch, position
}

func reconciliationDeliveryRequest(t *testing.T, repo *SetupManagerRepository, ctx context.Context, epoch, position uuid.UUID, scopes []string) {
	t.Helper()
	err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, scopes)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPublicationReconciliationDeliveryLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	rollbackErr := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, []string{"rollback"}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if rollbackErr == nil {
		t.Fatal("expected rollback")
	}
	var count int
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("rollback left request: count=%d err=%v", count, err)
	}
	positions := []uuid.UUID{position, position}
	scopes := []string{"album", "album", "release"}
	err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, positions, scopes)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 2 || len(scopes) != 3 {
		t.Fatal("request mutated caller slices")
	}
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"release"})
	var generation int64
	if err := repo.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &generation); err != nil || generation != 2 {
		t.Fatalf("generation=%d err=%v", generation, err)
	}
	var events int
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending' AND observed_generation=2`, epoch, position).Scan(ctx, &events); err != nil || events != 1 {
		t.Fatalf("coalesced events=%d err=%v", events, err)
	}
	inserter := new(reconciliationDeliveryInserter)
	n, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || n != 1 {
		t.Fatalf("dispatch=%d err=%v", n, err)
	}
	if len(inserter.args) != 1 || inserter.args[0].OperationID == uuid.Nil {
		t.Fatalf("unexpected args: %+v", inserter.args)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}
	_, result, err = repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || result != PublicationReconciliationBusy {
		t.Fatalf("duplicate claim=%s err=%v", result, err)
	}
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"new-scope"})
	n, err = repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || n != 0 {
		t.Fatalf("dispatch while active=%d err=%v", n, err)
	}
	var scopeCount int
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND scope='new-scope' AND generation=3`, epoch, position).Scan(ctx, &scopeCount); err != nil || scopeCount != 1 {
		t.Fatalf("newer scope missing: count=%d err=%v", scopeCount, err)
	}
	err = repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ok, err := repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatal("current guard rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND generation=2`, epoch, position).Scan(ctx, &scopeCount); err != nil || scopeCount != 0 {
		t.Fatalf("old scopes retained: %d %v", scopeCount, err)
	}
	var stale bool
	err = repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var e error
		stale, e = repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		return e
	})
	if err != nil || stale {
		t.Fatalf("completed guard replay accepted: %v %v", stale, err)
	}
	n, err = repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || n != 1 {
		t.Fatalf("successor dispatch=%d err=%v", n, err)
	}
}

func TestPublicationReconciliationDispatchDoesNotStarveEligiblePositionsPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, _ := reconciliationDeliveryFixture(t)
	const blockedCount = 64
	blocked := make([]uuid.UUID, blockedCount)
	for i := range blocked {
		blocked[i] = uuid.UUID{15: byte(i + 1)}
	}
	eligible := uuid.UUID{14: 1, 15: 1}
	release := uuid.New()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'starvation')`, release); err != nil {
		t.Fatal(err)
	}
	err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for index, position := range append(blocked, eligible) {
			track := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,'starvation')`, track).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,?)`, position, release, track, index).Exec(ctx); err != nil {
				return err
			}
			generation := int64(1)
			if index < blockedCount {
				generation = 2
			}
			if _, err := tx.NewRaw(`INSERT INTO publication_reconciliation_request(output_epoch,position_id,requested_generation) VALUES (?,?,?)`, epoch, position, generation).Exec(ctx); err != nil {
				return err
			}
			oldOp, oldEvent := uuid.New(), uuid.New()
			oldSnapshot, _ := json.Marshal(PublicationReconciliationSnapshot{Version: 1, OutputEpoch: epoch, PositionID: position})
			if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,?,'queued','queued',?::jsonb)`, oldOp, PublicationReconciliationOperationKind, string(oldSnapshot)).Exec(ctx); err != nil {
				return err
			}
			if index == blockedCount {
				_, err := tx.NewRaw(`INSERT INTO publication_reconciliation_event(id,output_epoch,position_id,operation_id,observed_generation,state) VALUES (?,?,?,?,1,'pending')`, oldEvent, epoch, position, oldOp).Exec(ctx)
				return err
			}
			var jobID int64
			jobArgs, _ := json.Marshal(PublicationReconciliationJobArgs{OperationID: oldOp})
			if err := tx.NewRaw(`INSERT INTO river_job(kind,args,state,attempt) VALUES (?,?::jsonb,'available',0) RETURNING id`, PublicationReconciliationOperationKind, string(jobArgs)).Scan(ctx, &jobID); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`UPDATE operation SET river_job_id=? WHERE id=?`, jobID, oldOp).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO publication_reconciliation_event(id,output_epoch,position_id,operation_id,observed_generation,state,registered_job_id,delivery_epoch) VALUES (?,?, ?, ?,1,'dispatched',?,1)`, oldEvent, epoch, position, oldOp, jobID).Exec(ctx); err != nil {
				return err
			}
			if index < blockedCount {
				pendingOp, pendingEvent := uuid.New(), uuid.New()
				if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,?,'queued','queued',?::jsonb)`, pendingOp, PublicationReconciliationOperationKind, string(oldSnapshot)).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`INSERT INTO publication_reconciliation_event(id,output_epoch,position_id,operation_id,observed_generation,state) VALUES (?,?, ?, ?,2,'pending')`, pendingEvent, epoch, position, pendingOp).Exec(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	inserter := new(reconciliationDeliveryInserter)
	count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter)
	if err != nil || count != 1 {
		t.Fatalf("dispatch eligible beyond 64 busy positions: count=%d err=%v", count, err)
	}
	var state string
	if err := repo.db.NewRaw(`SELECT state FROM publication_reconciliation_event WHERE position_id=? AND observed_generation=1 AND state='dispatched' ORDER BY created_at DESC LIMIT 1`, eligible).Scan(ctx, &state); err != nil {
		t.Fatalf("eligible pending event was not dispatched: %v", err)
	}
	var pending int
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE position_id IN (?) AND state='pending'`, bun.In(blocked)).Scan(ctx, &pending); err != nil || pending != blockedCount {
		t.Fatalf("busy successors changed: pending=%d err=%v", pending, err)
	}
}
