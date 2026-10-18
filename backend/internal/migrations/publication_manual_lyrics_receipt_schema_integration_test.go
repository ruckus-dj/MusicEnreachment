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

func TestPublicationManualLyricsReceiptRollbackRefusesPopulatedTablePostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261018000001" {
			prior.Add(migration)
		} else if migration.Name == "20261018000001" {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("receipt target migrations = %d, want one", len(target.Sorted()))
	}
	priorMigrator := migrate.NewMigrator(database, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	targetMigrator := migrate.NewMigrator(database, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'receipt rollback')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'receipt rollback')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO publication_manual_lyrics_receipt
		(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256)
		VALUES (?,?,0,?,?,'value',repeat('a',64))`, positionID, uuid.New(), uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "refusing to drop populated publication_manual_lyrics_receipt") {
		t.Fatalf("rollback with populated receipt table = %v, want guarded refusal", err)
	}
	var retained bool
	if err := database.NewRaw(`SELECT to_regclass('public.publication_manual_lyrics_receipt') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("receipt table retained after refused rollback = %t, %v", retained, err)
	}
}
