//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestSourceCleanupOwnerSchemaPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261023000001" {
			prior.Add(migration)
		} else if migration.Name == "20261023000001" {
			target.Add(migration)
		}
	}
	for _, group := range []*migrate.Migrations{prior, target} {
		migrator := migrate.NewMigrator(db, group, migrate.WithMarkAppliedOnSuccess(true))
		if err := migrator.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := migrator.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	targetMigrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))

	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id) VALUES (?)`, []any{releaseID}},
		{`INSERT INTO local_track(id) VALUES (?)`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,"position",track_id,recording_id) VALUES (?,?,1,?,NULL)`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	nextJobID := int64(1000)
	makeOwner := func(t *testing.T) (uuid.UUID, uuid.UUID, uuid.UUID, int64) {
		t.Helper()
		nextJobID++
		opID, itemID, epoch := uuid.New(), uuid.New(), uuid.New()
		snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epoch.String() + `"}`
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id,finished_at)
			VALUES (?,'cleanup_publication_sources_v1','succeeded','complete',?::jsonb,?,now())`, opID, snapshot, nextJobID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewRaw(`INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state)
			VALUES (?,?,?,?,?,'cancelled')`, itemID, opID, positionID, epoch, uuid.New()).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return opID, itemID, epoch, nextJobID
	}
	operationID, itemID, epoch, jobID := makeOwner(t)
	ownerID, rootID, locationID, workID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insert := func(item, operation uuid.UUID, attempt int, job int64, owner, root, location, work uuid.UUID) error {
		_, err := db.NewRaw(`INSERT INTO source_cleanup_owner
			(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id)
			VALUES (?,?,?, ?,?,?,?,?)`, item, operation, attempt, job, owner, root, location, work).Exec(ctx)
		return err
	}
	if err := insert(itemID, operationID, 1, jobID, ownerID, rootID, locationID, workID); err != nil {
		t.Fatalf("insert exact owner: %v", err)
	}
	if err := insert(itemID, operationID, 2, jobID, uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil || !strings.Contains(err.Error(), "source_cleanup_owner_pkey") {
		t.Fatalf("duplicate item owner error = %v", err)
	}
	otherOperationID, otherItemID, _, otherJobID := makeOwner(t)
	if err := insert(otherItemID, operationID, 1, jobID, uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil || !strings.Contains(err.Error(), "source_cleanup_owner_item_fkey") {
		t.Fatalf("item paired with another valid operation error = %v", err)
	}
	if err := insert(otherItemID, otherOperationID, 1, jobID, uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil || !strings.Contains(err.Error(), "source_cleanup_owner_operation_fkey") {
		t.Fatalf("owner with another operation's River job ID error = %v", err)
	}
	wrongAttemptItem := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state)
		VALUES (?,?,?,?,?,'cancelled')`, wrongAttemptItem, operationID, positionID, epoch, uuid.New()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := insert(wrongAttemptItem, operationID, 2, jobID, uuid.New(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("owner with wrong application attempt succeeded")
	}
	otherOwnerID, otherRootID, otherLocationID, otherWorkID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := insert(otherItemID, otherOperationID, 1, otherJobID, otherOwnerID, otherRootID, otherLocationID, otherWorkID); err != nil {
		t.Fatalf("insert distinct valid owner: %v", err)
	}
	for _, duplicate := range []struct {
		name                        string
		owner, root, location, work uuid.UUID
	}{
		{"work", uuid.New(), uuid.New(), uuid.New(), workID},
		{"location", uuid.New(), uuid.New(), locationID, uuid.New()},
	} {
		duplicateOperationID, duplicateItemID, _, duplicateJobID := makeOwner(t)
		constraint := "source_cleanup_owner_work_id_key"
		if duplicate.name == "location" {
			constraint = "source_cleanup_owner_source_location_id_key"
		}
		if err := insert(duplicateItemID, duplicateOperationID, 1, duplicateJobID, duplicate.owner, duplicate.root, duplicate.location, duplicate.work); err == nil || !strings.Contains(err.Error(), constraint) {
			t.Fatalf("duplicate %s constraint error = %v, want %s", duplicate.name, err, constraint)
		}
	}
	if _, err := db.NewRaw(`DELETE FROM source_cleanup_item WHERE id=?`, itemID).Exec(ctx); err == nil {
		t.Fatal("deleting an owned item succeeded")
	}
	if _, err := db.NewRaw(`DELETE FROM operation WHERE id=?`, operationID).Exec(ctx); err == nil {
		t.Fatal("deleting the owner operation succeeded")
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "while owner rows exist") {
		t.Fatalf("populated owner rollback error = %v", err)
	}
	var ownerCount, constraintCount int
	if err := db.NewRaw(`SELECT count(*) FROM source_cleanup_owner`).Scan(ctx, &ownerCount); err != nil || ownerCount != 2 {
		t.Fatalf("rollback lost owner rows: count=%d err=%v", ownerCount, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM pg_constraint WHERE conrelid='public.source_cleanup_item'::regclass AND conname='source_cleanup_item_id_operation_key'`).Scan(ctx, &constraintCount); err != nil || constraintCount != 1 {
		t.Fatalf("refused rollback changed item constraint: count=%d err=%v", constraintCount, err)
	}
	if _, err := db.NewRaw(`DELETE FROM source_cleanup_owner`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Rollback(ctx); err != nil {
		t.Fatalf("empty owner rollback: %v", err)
	}
	var tableExists bool
	if err := db.NewRaw(`SELECT to_regclass('public.source_cleanup_owner') IS NOT NULL`).Scan(ctx, &tableExists); err != nil || tableExists {
		t.Fatalf("owner table remains after rollback: exists=%t err=%v", tableExists, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM pg_constraint WHERE conrelid='public.source_cleanup_item'::regclass AND conname='source_cleanup_item_id_operation_key'`).Scan(ctx, &constraintCount); err != nil || constraintCount != 0 {
		t.Fatalf("empty rollback retained item constraint: count=%d err=%v", constraintCount, err)
	}
}
