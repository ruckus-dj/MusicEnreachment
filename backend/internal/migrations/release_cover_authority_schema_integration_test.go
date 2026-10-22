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

const releaseCoverAuthorityMigrationName = "20261029000001"

func TestReleaseCoverAuthorityMigrationAndGuardedRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior := migrate.NewMigrations()
	target := migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < releaseCoverAuthorityMigrationName {
			prior.Add(migration)
		} else if migration.Name == releaseCoverAuthorityMigrationName {
			target.Add(migration)
		}
	}
	priorMigrator := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	migrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	releaseID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id) VALUES (?)`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_publication_cover(release_id) VALUES (?)`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "refusing to discard current release cover authority") {
		t.Fatalf("rollback with retained cover authority = %v, want guarded refusal", err)
	}
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM release_publication_cover WHERE release_id=?`, releaseID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("cover authority after refused rollback = %d, %v", count, err)
	}
	if _, err := db.NewRaw(`DELETE FROM release_publication_cover WHERE release_id=?`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Rollback(ctx); err != nil {
		t.Fatalf("rollback after authority was explicitly removed: %v", err)
	}
}
