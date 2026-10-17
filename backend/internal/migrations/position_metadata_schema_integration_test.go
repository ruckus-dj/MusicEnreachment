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

const positionMetadataMigrationName = "20261017000004"

type positionMetadataRow struct {
	ID       uuid.UUID      `bun:"id"`
	Position int            `bun:"position"`
	Metadata map[string]any `bun:"metadata,type:jsonb"`
}

// TestPositionMetadataMigrationWithPostgreSQL applies the migrations before the
// metadata change, seeds a release track, and applies only the embedded target
// migration. That isolates its default, its populated-metadata rollback guard,
// and the clean rollback that must keep the position identity intact.
func TestPositionMetadataMigrationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	prior := migrate.NewMigrations()
	target := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		switch {
		case migration.Name < positionMetadataMigrationName:
			prior.Add(migration)
		case migration.Name == positionMetadataMigrationName:
			target.Add(migration)
		}
	}
	if names := positionMetadataMigrationNames(target.Sorted()); len(names) != 1 || names[0] != positionMetadataMigrationName {
		t.Fatalf("target collection = %v, want only %s", names, positionMetadataMigrationName)
	}

	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize prior migrations: %v", err)
	}
	group, err := priorMigrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply prior migrations: %v", err)
	}
	if applied := positionMetadataGroupNames(group); len(applied) == 0 || applied[len(applied)-1] != "20261017000003" {
		t.Fatalf("prior migration group = %v, want it to end with 20261017000003", applied)
	}
	if positionMetadataColumnExists(t, database) {
		t.Fatal("release_track.metadata exists before the position metadata migration")
	}

	releaseID := uuid.New()
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release (id) VALUES (?)`, releaseID); err != nil {
		t.Fatalf("insert local release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track (id) VALUES (?)`, trackID); err != nil {
		t.Fatalf("insert local track: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO release_track
		(release_id, "position", track_id, recording_id) VALUES (?, 1, ?, NULL)`, releaseID, trackID); err != nil {
		t.Fatalf("insert baseline release track: %v", err)
	}
	var positionID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM release_track
		WHERE release_id = ? AND medium = 0 AND "position" = 1`, releaseID).Scan(ctx, &positionID); err != nil {
		t.Fatalf("read baseline release track id: %v", err)
	}

	targetMigrator := migrate.NewMigrator(database, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatalf("initialize position metadata migration: %v", err)
	}
	group, err = targetMigrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("apply position metadata migration: %v", err)
	}
	if names := positionMetadataGroupNames(group); len(names) != 1 || names[0] != positionMetadataMigrationName {
		t.Fatalf("applied migration group = %v, want only %s", names, positionMetadataMigrationName)
	}
	var laterState *string
	if err := database.NewRaw(`SELECT to_regclass('public.publication_namespace_state')::text`).Scan(ctx, &laterState); err != nil || laterState != nil {
		t.Fatalf("later migration state after target-only apply = %v, %v; want none", laterState, err)
	}

	var dataType, nullable string
	var defaultValue *string
	if err := database.NewRaw(`SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'release_track' AND column_name = 'metadata'`).
		Scan(ctx, &dataType, &nullable, &defaultValue); err != nil {
		t.Fatalf("read release_track.metadata definition: %v", err)
	}
	if dataType != "jsonb" || nullable != "NO" || defaultValue == nil || !strings.Contains(*defaultValue, "'{}'::jsonb") {
		t.Fatalf("release_track.metadata definition = %s/%s/%v, want jsonb/NOT NULL/'{}'::jsonb", dataType, nullable, defaultValue)
	}
	row := positionMetadataRead(t, database, positionID)
	if row.ID != positionID || row.Position != 1 || len(row.Metadata) != 0 {
		t.Fatalf("baseline metadata row = %+v, want id %s at position 1 with an empty object", row, positionID)
	}

	if _, err := database.ExecContext(ctx, `UPDATE release_track SET metadata = '{"source":"manual"}'::jsonb
		WHERE id = ?`, positionID); err != nil {
		t.Fatalf("populate release track metadata: %v", err)
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back position metadata") {
		t.Fatalf("target rollback with populated metadata = %v, want a refusal", err)
	}
	if !positionMetadataColumnExists(t, database) {
		t.Fatal("release_track.metadata was removed by the refused rollback")
	}
	if row = positionMetadataRead(t, database, positionID); row.Metadata["source"] != "manual" {
		t.Fatalf("metadata after refused rollback = %v, want the populated object preserved", row.Metadata)
	}

	if _, err := database.ExecContext(ctx, `UPDATE release_track SET metadata = '{}'::jsonb
		WHERE id = ?`, positionID); err != nil {
		t.Fatalf("clear release track metadata: %v", err)
	}
	rolledBack, err := targetMigrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("rollback position metadata migration: %v", err)
	}
	if names := positionMetadataGroupNames(rolledBack); len(names) != 1 || names[0] != positionMetadataMigrationName {
		t.Fatalf("rolled back migration group = %v, want only %s", names, positionMetadataMigrationName)
	}
	if positionMetadataColumnExists(t, database) {
		t.Fatal("release_track.metadata remains after rollback")
	}
	var id uuid.UUID
	var position int
	if err := database.NewRaw(`SELECT id, "position" FROM release_track WHERE release_id = ?`, releaseID).
		Scan(ctx, &id, &position); err != nil || id != positionID || position != 1 {
		t.Fatalf("release track after rollback = %s/%d, %v; want %s at position 1", id, position, err, positionID)
	}

	group, err = targetMigrator.Migrate(ctx)
	if err != nil {
		t.Fatalf("reapply position metadata migration: %v", err)
	}
	if names := positionMetadataGroupNames(group); len(names) != 1 || names[0] != positionMetadataMigrationName {
		t.Fatalf("reapplied migration group = %v, want only %s", names, positionMetadataMigrationName)
	}
	if row = positionMetadataRead(t, database, positionID); len(row.Metadata) != 0 {
		t.Fatalf("reapplied metadata = %v, want the empty object default", row.Metadata)
	}
}

func positionMetadataRead(t *testing.T, database *bun.DB, id uuid.UUID) positionMetadataRow {
	t.Helper()
	var row positionMetadataRow
	if err := database.NewRaw(`SELECT id, "position", metadata FROM release_track WHERE id = ?`, id).
		Scan(context.Background(), &row); err != nil {
		t.Fatalf("read release track metadata: %v", err)
	}
	return row
}

func positionMetadataColumnExists(t *testing.T, database *bun.DB) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'release_track' AND column_name = 'metadata'`).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read release_track.metadata: %v", err)
	}
	return count == 1
}

func positionMetadataGroupNames(group *migrate.MigrationGroup) []string {
	if group == nil {
		return nil
	}
	names := make([]string, len(group.Migrations))
	for index, migration := range group.Migrations {
		names[index] = migration.Name
	}
	return names
}

func positionMetadataMigrationNames(list migrate.MigrationSlice) []string {
	names := make([]string, len(list))
	for index, migration := range list {
		names[index] = migration.Name
	}
	return names
}
