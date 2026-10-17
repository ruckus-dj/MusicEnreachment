//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const releaseTrackIdentityMigrationName = "20261017000002"

// TestReleaseTrackIdentityMigrationWithPostgreSQL applies every migration that
// precedes the identity change, seeds release tracks in the pre-change shape,
// and only then applies the new migration. That ordering is what proves the
// one-time backfill of existing rows; applying the current schema first would
// leave nothing to backfill.
func TestReleaseTrackIdentityMigrationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}

	prior := migrate.NewMigrations()
	throughTarget := migrate.NewMigrations()
	targetFound := false
	for _, migration := range collection.Sorted() {
		switch {
		case migration.Name == releaseTrackIdentityMigrationName:
			targetFound = true
			throughTarget.Add(migration)
		case migration.Name < releaseTrackIdentityMigrationName:
			prior.Add(migration)
			throughTarget.Add(migration)
		}
	}
	if !targetFound {
		t.Fatalf("migration %s is not embedded", releaseTrackIdentityMigrationName)
	}

	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize prior migrations: %v", err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatalf("apply prior migrations: %v", err)
	}
	if releaseTrackColumnExists(t, database, "id") {
		t.Fatal("release_track.id exists before the identity migration")
	}

	releaseID := uuid.New()
	trackID := uuid.New()
	recordingID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release (id) VALUES (?)`, releaseID); err != nil {
		t.Fatalf("insert local release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track (id) VALUES (?)`, trackID); err != nil {
		t.Fatalf("insert local track: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_recording (id) VALUES (?)`, recordingID); err != nil {
		t.Fatalf("insert local recording: %v", err)
	}
	// Two rows of the same release share one local track at distinct positions.
	// This is the existing composite-primary-key shape that must keep working.
	if _, err := database.ExecContext(ctx, `INSERT INTO release_track
		(release_id, "position", track_id, recording_id, medium, displayed_number)
		VALUES (?, 1, ?, ?, 0, '1'), (?, 2, ?, NULL, 0, '2')`,
		releaseID, trackID, recordingID, releaseID, trackID); err != nil {
		t.Fatalf("insert pre-migration release tracks: %v", err)
	}

	migrator := migrate.NewMigrator(database, throughTarget, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize identity migration: %v", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply identity migration: %v", err)
	}
	if group == nil || len(group.Migrations) != 1 || group.Migrations[0].Name != releaseTrackIdentityMigrationName {
		t.Fatalf("applied migration group = %v, want only %s", releaseTrackMigrationNames(group), releaseTrackIdentityMigrationName)
	}

	var dataType, nullable string
	var defaultValue *string
	if err := database.NewRaw(`SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'release_track' AND column_name = 'id'`).
		Scan(ctx, &dataType, &nullable, &defaultValue); err != nil {
		t.Fatalf("read release_track.id definition: %v", err)
	}
	if dataType != "uuid" {
		t.Fatalf("release_track.id type = %q, want uuid", dataType)
	}
	if nullable != "NO" {
		t.Fatalf("release_track.id nullable = %q, want NO", nullable)
	}
	if defaultValue == nil || !strings.Contains(*defaultValue, "gen_random_uuid") {
		t.Fatalf("release_track.id default = %v, want gen_random_uuid()", defaultValue)
	}
	if !releaseTrackConstraintExists(t, database, "release_track_id_key") {
		t.Fatal("release_track_id_key unique constraint is missing")
	}
	requireReleaseTrackPrimaryKey(t, database)

	var seeded []struct {
		ID       uuid.UUID `bun:"id"`
		Position int       `bun:"position"`
	}
	if err := database.NewRaw(`SELECT id, "position" FROM release_track
		WHERE release_id = ? ORDER BY medium, "position"`, releaseID).
		Scan(ctx, &seeded); err != nil {
		t.Fatalf("read backfilled release tracks: %v", err)
	}
	if len(seeded) != 2 {
		t.Fatalf("backfilled release track rows = %d, want 2", len(seeded))
	}
	if seeded[0].ID == uuid.Nil || seeded[1].ID == uuid.Nil {
		t.Fatal("backfill left a NULL or zero UUID on an existing release track")
	}
	if seeded[0].ID == seeded[1].ID {
		t.Fatalf("backfill reused UUID %s for distinct release tracks", seeded[0].ID)
	}

	if _, err := database.ExecContext(ctx, `INSERT INTO release_track
		(release_id, "position", track_id) VALUES (?, 3, ?)`, releaseID, trackID); err != nil {
		t.Fatalf("insert release track without explicit id: %v", err)
	}
	var generatedID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM release_track
		WHERE release_id = ? AND medium = 0 AND "position" = 3`, releaseID).
		Scan(ctx, &generatedID); err != nil {
		t.Fatalf("read generated release track id: %v", err)
	}
	if generatedID == uuid.Nil || generatedID == seeded[0].ID || generatedID == seeded[1].ID {
		t.Fatalf("generated release track id = %s, want a fresh UUID", generatedID)
	}

	requireReleaseTrackViolation(t, releaseTrackExecError(database, `INSERT INTO release_track
		(id, release_id, "position", track_id) VALUES (?, ?, 4, ?)`,
		seeded[0].ID, releaseID, trackID), "release_track_id_key")

	if _, err := database.ExecContext(ctx, `UPDATE release_track SET displayed_number = '1 revised'
		WHERE release_id = ? AND medium = 0 AND "position" = 1`, releaseID); err != nil {
		t.Fatalf("update displayed number: %v", err)
	}
	var stableID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM release_track
		WHERE release_id = ? AND medium = 0 AND "position" = 1`, releaseID).
		Scan(ctx, &stableID); err != nil {
		t.Fatalf("read release track id after update: %v", err)
	}
	if stableID != seeded[0].ID {
		t.Fatalf("release track id after displayed_number update = %s, want %s", stableID, seeded[0].ID)
	}

	// A dependent foreign key turns the down migration into a safe failure:
	// the down file must not use CASCADE, so PostgreSQL refuses to remove the
	// referenced unique constraint and the column stays in place.
	if _, err := database.ExecContext(ctx, `CREATE TABLE public.release_track_identity_probe (
		identity uuid NOT NULL REFERENCES public.release_track(id))`); err != nil {
		t.Fatalf("create dependent foreign key fixture: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO public.release_track_identity_probe (identity)
		VALUES (?)`, seeded[0].ID); err != nil {
		t.Fatalf("insert dependent foreign key fixture: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("down migration removed a referenced identity column, want a dependent-object error")
	}
	if !releaseTrackColumnExists(t, database, "id") {
		t.Fatal("release_track.id was removed by the refused down migration")
	}
	if !releaseTrackConstraintExists(t, database, "release_track_id_key") {
		t.Fatal("release_track_id_key was removed by the refused down migration")
	}
	requireReleaseTrackPrimaryKey(t, database)
	if rows := countReleaseTracks(t, database); rows != 3 {
		t.Fatalf("release track rows after refused down migration = %d, want 3", rows)
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE public.release_track_identity_probe`); err != nil {
		t.Fatalf("drop dependent foreign key fixture: %v", err)
	}

	rolledBack, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback identity migration: %v", err)
	}
	if rolledBack == nil || len(rolledBack.Migrations) != 1 || rolledBack.Migrations[0].Name != releaseTrackIdentityMigrationName {
		t.Fatalf("rolled back migration group = %v, want only %s", releaseTrackMigrationNames(rolledBack), releaseTrackIdentityMigrationName)
	}
	if releaseTrackColumnExists(t, database, "id") {
		t.Fatal("release_track.id remains after rollback")
	}
	if releaseTrackConstraintExists(t, database, "release_track_id_key") {
		t.Fatal("release_track_id_key remains after rollback")
	}
	requireReleaseTrackPrimaryKey(t, database)
	if rows := countReleaseTracks(t, database); rows != 3 {
		t.Fatalf("release track rows after rollback = %d, want 3 unchanged rows", rows)
	}
}

func releaseTrackMigrationNames(group *migrate.MigrationGroup) []string {
	if group == nil {
		return nil
	}
	names := make([]string, len(group.Migrations))
	for i, migration := range group.Migrations {
		names[i] = migration.Name
	}
	return names
}

func releaseTrackColumnExists(t *testing.T, database *bun.DB, column string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'release_track' AND column_name = ?`, column).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read release_track.%s: %v", column, err)
	}
	return count == 1
}

func releaseTrackConstraintExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'public.release_track'::regclass AND conname = ?`, name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read release_track constraint %s: %v", name, err)
	}
	return count == 1
}

func requireReleaseTrackPrimaryKey(t *testing.T, database *bun.DB) {
	t.Helper()
	var columns []string
	if err := database.NewRaw(`SELECT attribute.attname
		FROM pg_constraint AS con
		CROSS JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS key(attnum, ordinal)
		JOIN pg_attribute AS attribute
			ON attribute.attrelid = con.conrelid AND attribute.attnum = key.attnum
		WHERE con.conrelid = 'public.release_track'::regclass
			AND con.contype = 'p'
		ORDER BY key.ordinal`).Scan(context.Background(), &columns); err != nil {
		t.Fatalf("read release_track primary key: %v", err)
	}
	if got := strings.Join(columns, ","); got != "release_id,medium,position" {
		t.Fatalf("release_track primary key columns = %q, want release_id,medium,position", got)
	}
}

func countReleaseTracks(t *testing.T, database *bun.DB) int {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM release_track`).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("count release tracks: %v", err)
	}
	return count
}

func releaseTrackExecError(database *bun.DB, query string, args ...any) error {
	_, err := database.ExecContext(context.Background(), query, args...)
	return err
}

func requireReleaseTrackViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("statement succeeded, want a %s violation", constraint)
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("error = %v, want a %s violation", err, constraint)
	}
}
