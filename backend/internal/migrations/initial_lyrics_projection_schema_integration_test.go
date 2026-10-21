//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestInitialLyricsProjectionMigrationSchemaPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	var columns int
	if err := database.NewRaw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='track_publication'
		AND column_name='initial_lyrics' AND data_type='jsonb' AND is_nullable='YES'`).Scan(ctx, &columns); err != nil || columns != 1 {
		t.Fatalf("nullable initial_lyrics JSONB column count = %d, %v; want one", columns, err)
	}
	var constraints int
	if err := database.NewRaw(`SELECT count(*) FROM pg_constraint
		WHERE conrelid='public.track_publication'::regclass
		AND conname='track_publication_initial_lyrics_object'`).Scan(ctx, &constraints); err != nil || constraints != 1 {
		t.Fatalf("initial_lyrics object constraint count = %d, %v; want one", constraints, err)
	}
}
