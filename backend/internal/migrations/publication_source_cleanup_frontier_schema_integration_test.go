//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestPublicationSourceCleanupMutationFrontierRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, m := range collection.Sorted() {
		if m.Name < "20261024000001" {
			prior.Add(m)
		} else if m.Name == "20261024000001" {
			target.Add(m)
		}
	}
	for _, group := range []*migrate.Migrations{prior, target} {
		mgr := migrate.NewMigrator(db, group, migrate.WithMarkAppliedOnSuccess(true))
		if err := mgr.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mgr := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	river, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	op, item, claim, position, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	jobID := int64(0)
	if err := db.QueryRowContext(ctx, `INSERT INTO river_job(kind,args) VALUES ('cleanup_publication_sources_v1',jsonb_build_object('operation_id',?::text)) RETURNING id`, op).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	releaseID, trackID := uuid.New(), uuid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id) VALUES (?)`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id) VALUES (?)`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,"position",track_id) VALUES (?,?,0,?)`, position, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `"}`
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id,started_at) VALUES (?, 'cleanup_publication_sources_v1','running','running',?::jsonb,1,?,now())`, op, snapshot, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO publication_execution_claim(operation_id,application_attempt,river_job_id,river_attempt,claim_uuid,boot_uuid,delivery_token,epoch,state) VALUES (?,1,?,1,?,?, 'test',?,'executing')`, op, jobID, claim, uuid.New(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision,mutation_claim_uuid,mutation_started_at) VALUES (?,?,?, ?,?,'claimed','{}'::jsonb,?,?,now())`, item, op, position, epoch, generation, uuid.New(), claim); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "frontier fields are populated") {
		t.Fatalf("populated frontier rollback error = %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM source_cleanup_item WHERE id=? AND mutation_claim_uuid=?`, item, claim).Scan(&count); err != nil || count != 1 {
		t.Fatalf("frontier item preserved: count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM publication_execution_claim WHERE operation_id=? AND claim_uuid=?`, op, claim).Scan(&count); err != nil || count != 1 {
		t.Fatalf("claim preserved: count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='public.source_cleanup_item'::regclass AND conname IN ('source_cleanup_item_mutation_frontier_check','source_cleanup_item_id_operation_key')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("schema constraints preserved: count=%d err=%v", count, err)
	}
	// Explicitly clear test data to exercise the supported empty-frontier rollback.
	if _, err := db.ExecContext(ctx, `UPDATE source_cleanup_item SET mutation_claim_uuid=NULL,mutation_started_at=NULL,mutation_completed_at=NULL WHERE id=?`, item); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Rollback(ctx); err != nil {
		t.Fatalf("empty frontier rollback: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='source_cleanup_item' AND column_name IN ('mutation_claim_uuid','mutation_started_at','mutation_completed_at')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("frontier columns remain: count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='public.source_cleanup_item'::regclass AND conname='source_cleanup_item_id_operation_key' AND contype='u'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("prior item unique constraint changed: count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='public.publication_execution_claim'::regclass AND conname='publication_execution_claim_operation_fkey' AND contype='f'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("prior claim foreign key changed: count=%d err=%v", count, err)
	}
}
