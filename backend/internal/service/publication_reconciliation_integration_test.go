//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

// reconciliationIntegrationInserter persists test deliveries in River's real
// table while retaining the returned row identities for assertions.
type reconciliationIntegrationInserter struct{}

func (reconciliationIntegrationInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	queue := "default"
	if opts != nil && opts.Queue != "" {
		queue = opts.Queue
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt,queue) VALUES ($1,$2,'available',0,$3) RETURNING id`, args.Kind(), string(encoded), queue).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func TestPublicationReconciliationRuntimeConsumerInitialPublicationPostgreSQL(t *testing.T) {
	var ran bool
	testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, _ uuid.UUID, epoch uuid.UUID, publications *LibraryPublications) {
		ran = true
		ctx := context.Background()
		repo := persistence.NewSetupManagerRepository(db)
		inserter := reconciliationIntegrationInserter{}
		publications.river = inserter
		// Record the genuine producer request, then dispatch it to an actual River row.
		if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return repo.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{positionID}, []string{"initial-runtime-consumer"})
		}); err != nil {
			t.Fatal(err)
		}
		if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
			t.Fatalf("dispatch count=%d err=%v", count, err)
		}
		var operationID uuid.UUID
		var riverJobID int64
		if err := db.NewRaw(`SELECT e.operation_id,e.registered_job_id FROM publication_reconciliation_event e WHERE e.output_epoch=? AND e.position_id=? AND e.state='dispatched'`, epoch, positionID).Scan(ctx, &operationID, &riverJobID); err != nil {
			t.Fatal(err)
		}
		if riverJobID <= 0 {
			t.Fatalf("registered River job id=%d", riverJobID)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, riverJobID); err != nil {
			t.Fatal(err)
		}
		guard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, operationID, riverJobID, 1)
		if err != nil || result != persistence.PublicationReconciliationClaimed {
			t.Fatalf("claim result=%v err=%v", result, err)
		}
		if err := publications.ReconcilePublication(ctx, guard); err != nil {
			t.Fatalf("reconcile initial publication: %v", err)
		}
		var state string
		if err := db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &state); err != nil || state != "succeeded" {
			t.Fatalf("reconciliation operation state=%q err=%v", state, err)
		}
		var attempts, claims, jobs int
		if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, positionID).Scan(ctx, &attempts); err != nil {
			t.Fatal(err)
		}
		if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_claim WHERE epoch=?`, epoch).Scan(ctx, &claims); err != nil {
			t.Fatal(err)
		}
		if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1'`).Scan(ctx, &jobs); err != nil {
			t.Fatal(err)
		}
		if attempts != 1 || claims == 0 || jobs != 1 {
			t.Fatalf("admission artifacts attempts=%d claims=%d publish jobs=%d", attempts, claims, jobs)
		}
		var requested, reconciled int64
		if err := db.NewRaw(`SELECT requested_generation,reconciled_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, positionID).Scan(ctx, &requested, &reconciled); err != nil {
			t.Fatal(err)
		}
		if requested != 1 || reconciled != 1 {
			t.Fatalf("request generations requested=%d reconciled=%d", requested, reconciled)
		}
	})
	if !ran {
		t.Fatal("PostgreSQL integration fixture did not run")
	}
}
