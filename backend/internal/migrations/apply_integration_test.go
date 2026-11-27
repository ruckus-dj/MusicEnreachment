//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestApplyInstallsBaselineAndIsIdempotent(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()

	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatalf("apply baseline migrations: %v", err)
	}
	if relationName(t, db, "operation") == nil {
		t.Fatal("Apply did not create the application schema")
	}
	assertBaselineHistory(t, db, 1)

	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatalf("reapply baseline migrations: %v", err)
	}
	assertBaselineHistory(t, db, 1)
	if relationName(t, db, "operation") == nil {
		t.Fatal("reapplying the baseline removed the application schema")
	}
}

func TestApplyRejectsUnknownAppliedMigrationWithoutTouchingSchema(t *testing.T) {
	for _, applied := range []struct {
		name   string
		reason string
	}{
		{name: "20261015000001", reason: "historical migration name"},
		{name: "20261127000300", reason: "superseded pre-release baseline"},
		{name: "20261127000400", reason: "superseded pre-release baseline"},
		{name: "20990101000000", reason: "unknown newer migration name"},
	} {
		t.Run(applied.name, func(t *testing.T) {
			t.Parallel()
			db := testpostgres.Open(t)
			testpostgres.Reset(t, db)
			ctx := context.Background()

			collection := loadMigrations(t)
			migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
			if err := migrator.Init(ctx); err != nil {
				t.Fatalf("initialize migration history: %v", err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO bun_migrations(name,group_id,migrated_at) VALUES (?,1,now())`, applied.name); err != nil {
				t.Fatalf("seed %s migration history: %v", applied.reason, err)
			}
			var historyBefore int
			if err := db.NewRaw(`SELECT count(*) FROM bun_migrations`).Scan(ctx, &historyBefore); err != nil {
				t.Fatalf("count migration history before Apply: %v", err)
			}

			err := migrations.Apply(ctx, db)
			if err == nil || !strings.Contains(err.Error(), "unknown applied application migration") {
				t.Fatalf("Apply with %s error = %v, want unknown applied application migration rejection", applied.reason, err)
			}
			if !strings.Contains(err.Error(), "fresh database") {
				t.Fatalf("Apply rejection %q does not direct the operator to a fresh database", err)
			}

			for _, table := range []string{"app_setting", "operation", "local_artist", "source_root"} {
				if relationName(t, db, table) != nil {
					t.Errorf("Apply created application table %s despite rejecting migration history", table)
				}
			}
			var historyAfter, baselineRows int
			if err := db.NewRaw(`SELECT count(*) FROM bun_migrations`).Scan(ctx, &historyAfter); err != nil {
				t.Fatalf("count migration history after Apply: %v", err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM bun_migrations WHERE name=?`, baselineMigrationNames[0]).Scan(ctx, &baselineRows); err != nil {
				t.Fatalf("check baseline history after Apply: %v", err)
			}
			if historyAfter != historyBefore {
				t.Fatalf("migration history rows after rejected Apply = %d, want unchanged %d", historyAfter, historyBefore)
			}
			if baselineRows != 0 {
				t.Fatalf("baseline migration history rows after rejected Apply = %d, want 0", baselineRows)
			}
			var retained string
			if err := db.NewRaw(`SELECT name FROM bun_migrations WHERE name=?`, applied.name).Scan(ctx, &retained); err != nil || retained != applied.name {
				t.Fatalf("seeded migration history after rejected Apply = %q, %v; want it unchanged", retained, err)
			}
		})
	}
}

func assertBaselineHistory(t *testing.T, db *bun.DB, want int) {
	t.Helper()
	var rows int
	if err := db.NewRaw(`SELECT count(*) FROM bun_migrations WHERE name=?`, baselineMigrationNames[0]).Scan(context.Background(), &rows); err != nil {
		t.Fatalf("count baseline migration history: %v", err)
	}
	if rows != want {
		t.Fatalf("baseline migration history rows = %d, want %d", rows, want)
	}
}
