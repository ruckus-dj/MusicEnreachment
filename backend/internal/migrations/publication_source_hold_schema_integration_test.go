//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/google/uuid"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

const publicationSourceHoldMigrationName = "20261017000007"

func TestPublicationSourceHoldSchemaAndGuardedRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		switch {
		case migration.Name < publicationSourceHoldMigrationName:
			prior.Add(migration)
		case migration.Name == publicationSourceHoldMigrationName:
			target.Add(migration)
		}
	}
	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	group, err := priorMigrator.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if names := publicationStateGroupNames(group); len(names) == 0 || names[len(names)-1] != "20261017000006" {
		t.Fatalf("prior migration group = %v, want it to end with 20261017000006", names)
	}
	targetMigrator := migrate.NewMigrator(database, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	group, err = targetMigrator.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if names := publicationStateGroupNames(group); len(names) != 1 || names[0] != publicationSourceHoldMigrationName {
		t.Fatalf("target migration group = %v, want only %s", names, publicationSourceHoldMigrationName)
	}

	var columnCount, foreignKeyCount int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
	WHERE table_schema='public' AND table_name='publication_source_hold'`).Scan(ctx, &columnCount); err != nil || columnCount != 14 {
		t.Fatalf("publication source hold columns = %d, %v; want 14", columnCount, err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM pg_constraint
		WHERE conrelid='public.publication_source_hold'::regclass AND contype='f'`).Scan(ctx, &foreignKeyCount); err != nil || foreignKeyCount != 6 {
		t.Fatalf("publication source hold foreign keys = %d, %v; want 6", foreignKeyCount, err)
	}
	var sourceUniqueConstraints int
	if err := database.NewRaw(`SELECT count(*) FROM pg_constraint
		WHERE conrelid='public.publication_source_hold'::regclass AND contype='u'
		AND conname <> 'publication_source_hold_uuid_key'`).Scan(ctx, &sourceUniqueConstraints); err != nil || sourceUniqueConstraints != 0 {
		t.Fatalf("publication source hold source uniqueness constraints = %d, %v; want none", sourceUniqueConstraints, err)
	}

	// The ownership triples point at a real River delivery and matching publication
	// attempt so this exercises the composite foreign keys, not just table shape.
	river, err := rivermigrate.New(riverdatabasesql.New(database.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := database.NewRaw(`INSERT INTO river_job(kind,args) VALUES ('publication_source_hold_probe','{}') RETURNING id`).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release(id) VALUES (?)`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id) VALUES (?)`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO release_track(id,release_id,"position",track_id,recording_id) VALUES (?,?,1,?,NULL)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	operationID, epoch := uuid.New(), uuid.New()
	snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epoch.String() + `","intent_kind":"audio"}`
	if _, err := database.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id,finished_at)
		VALUES (?,'publish_track_v1','succeeded','complete',?::jsonb,?,now())`, operationID, snapshot, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO track_publication_attempt
		(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,namespace_manifest,mutation_journal)
		VALUES (?,?,?,?,'audio','committed','{}','[]')`, operationID, positionID, epoch, uuid.New()); err != nil {
		t.Fatal(err)
	}
	rootID, locationID, workID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name) VALUES (?,'/source','Source')`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,0)`, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
		VALUES (?,?,'track.flac',0,now(),1,'audio',?)`, locationID, rootID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
		VALUES (?,?,?,'/source','/inventory','track.flac',0,now(),false,?)`, workID, locationID, rootID, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO publication_source_hold
		(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,
		 configured_path,inventory_path,relative_path,size_bytes,mtime)
		VALUES (?,1,?,?,?, ?,?,?,?,'/source','/inventory','track.flac',0,now())`,
		operationID, jobID, epoch, uuid.New(), rootID, locationID, workID, variantID); err != nil {
		t.Fatalf("insert valid publication source hold: %v", err)
	}
	assertForeignKeyViolation := func(name, query string, args ...any) {
		t.Helper()
		_, err := database.ExecContext(ctx, query, args...)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23503" || postgresError.ConstraintName != name {
			t.Fatalf("expected foreign-key violation %s (23503), got %v", name, err)
		}
	}
	assertForeignKeyViolation("publication_source_hold_operation_fkey", `UPDATE publication_source_hold SET application_attempt=2 WHERE operation_id=?`, operationID)
	assertForeignKeyViolation("publication_source_hold_operation_fkey", `UPDATE publication_source_hold SET river_job_id=river_job_id+100000 WHERE operation_id=?`, operationID)
	assertForeignKeyViolation("publication_source_hold_attempt_fkey", `UPDATE publication_source_hold SET epoch=? WHERE operation_id=?`, uuid.New(), operationID)
	// Other RESTRICT references may reject the same deletion first. PostgreSQL
	// does not guarantee which constraint fires, so check both the declared
	// action and the observable refusal without depending on trigger order.
	for _, owner := range []struct {
		constraint string
		table      string
		column     string
		id         uuid.UUID
	}{
		{"publication_source_hold_operation_fkey", "operation", "id", operationID},
		{"publication_source_hold_attempt_fkey", "track_publication_attempt", "operation_id", operationID},
		{"publication_source_hold_root_fkey", "source_root", "id", rootID},
		{"publication_source_hold_location_fkey", "source_location", "id", locationID},
		{"publication_source_hold_work_fkey", "source_analysis_work", "id", workID},
		{"publication_source_hold_variant_fkey", "media_variant", "id", variantID},
	} {
		var action string
		if err := database.NewRaw(`SELECT confdeltype::text FROM pg_constraint WHERE conrelid='publication_source_hold'::regclass AND conname=?`, owner.constraint).Scan(ctx, &action); err != nil || action != "r" {
			t.Fatalf("%s delete action = %q, %v; want RESTRICT", owner.constraint, action, err)
		}
		_, err := database.ExecContext(ctx, "DELETE FROM "+owner.table+" WHERE "+owner.column+"=?", owner.id)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || (postgresError.Code != "23503" && postgresError.Code != "23001") {
			t.Fatalf("delete held %s must be restricted: %v", owner.table, err)
		}
		var retained int
		if err := database.NewRaw("SELECT count(*) FROM "+owner.table+" WHERE "+owner.column+"=?", owner.id).Scan(ctx, &retained); err != nil || retained != 1 {
			t.Fatalf("held %s retained = %d, %v", owner.table, retained, err)
		}
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back publication source holds while holds exist") {
		t.Fatalf("rollback with a live source hold = %v, want a refusal", err)
	}
	var retained bool
	if err := database.NewRaw(`SELECT to_regclass('public.publication_source_hold') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("refused rollback retained source hold table = %t, %v", retained, err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM publication_source_hold WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Rollback(ctx); err != nil {
		t.Fatalf("rollback after deleting source hold: %v", err)
	}
}
