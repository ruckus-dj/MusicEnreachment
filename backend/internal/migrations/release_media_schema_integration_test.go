//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestReleaseMediaRollbackRefusesToDiscardMediaMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply current migrations: %v", err)
	}
	releaseID, artistID := uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_artist(id) VALUES (?)`, artistID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'Rollback guard')`, releaseID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO release_artist(release_id,"position",artist_id) VALUES (?,0,?)`, releaseID, artistID)
		return err
	}); err != nil {
		t.Fatalf("create release fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_medium(release_id,medium,title) VALUES (?::uuid,0,'Disc')`, releaseID); err != nil {
		t.Fatalf("create populated media fixture: %v", err)
	}
	if _, err := migrator.Rollback(ctx); err == nil {
		t.Fatal("rollback discarded release medium metadata; expected loss guard")
	}
	var title string
	if err := db.NewRaw(`SELECT title FROM release_medium WHERE release_id=?::uuid AND medium=0`, releaseID).Scan(ctx, &title); err != nil || title != "Disc" {
		t.Fatalf("release medium after rejected rollback = %q, %v", title, err)
	}
}
