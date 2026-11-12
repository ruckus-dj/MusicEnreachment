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

func TestUnpublishedPublicationReconciliationAcknowledgesObservedGenerationPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"unpublished_reconciliation_test"})
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("dispatch unpublished reconciliation=%d err=%v", count, err)
	}
	operationID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, operationID, jobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim unpublished reconciliation=%s err=%v", result, err)
	}
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, []string{"provider_refresh"})
	}); err != nil {
		t.Fatal(err)
	}
	var release uuid.UUID
	if err := repo.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	acknowledged, err := repo.AcknowledgePublicationReconciliationUnpublished(ctx, release, guard)
	if err != nil || !acknowledged {
		t.Fatalf("acknowledge still-unpublished reconciliation=%v err=%v", acknowledged, err)
	}
	var current, attempts, dirty int
	var generation int64
	var eventState, operationState string
	if err := repo.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, position).Scan(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, position).Scan(ctx, &attempts); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT generation FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND scope='provider_refresh'`, epoch, position).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT state FROM publication_reconciliation_event WHERE operation_id=?`, operationID).Scan(ctx, &eventState); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState); err != nil {
		t.Fatal(err)
	}
	var reconciled int64
	if err := repo.db.NewRaw(`SELECT reconciled_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &reconciled); err != nil {
		t.Fatal(err)
	}
	if current != 0 || attempts != 0 || dirty != 1 || generation != guard.ObservedGeneration+1 || reconciled != guard.ObservedGeneration || eventState != "completed" || operationState != "succeeded" {
		t.Fatalf("unpublished acknowledgement changed authority: current=%d attempts=%d dirty=%d generation=%d reconciled=%d event=%s operation=%s", current, attempts, dirty, generation, reconciled, eventState, operationState)
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
	db := testpostgres.OpenMigratedWithRiver(t)
	initializeRiver(t, ctx, db)
	position, release, track := uuid.New(), uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'reconciliation')`, release); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, track); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, position, release, track); err != nil {
			return err
		}
		return seedRecordingCreditBaselineTx(ctx, tx, position, track, "track", "Reconciliation artist")
	}); err != nil {
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
