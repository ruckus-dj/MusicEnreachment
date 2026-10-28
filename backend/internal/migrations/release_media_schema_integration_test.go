//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
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
	var releaseID string
	if err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (gen_random_uuid(),'Rollback guard') RETURNING id`).Scan(ctx, &releaseID); err != nil {
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
