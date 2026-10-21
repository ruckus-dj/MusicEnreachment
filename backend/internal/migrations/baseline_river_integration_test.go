//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/uptrace/bun/migrate"

	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCurrentSchemaRollbackPreservesRiverAndUnrelatedData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.Open(t)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	application := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := application.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var tableCount int
	if err := database.NewRaw(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind='r' AND c.relname NOT LIKE 'bun\_%' ESCAPE '\'`).Scan(ctx, &tableCount); err != nil {
		t.Fatal(err)
	}
	// The five baselines contain 85 tables; later publication, reconciliation,
	// cleanup ownership, explicit retry, per-item resolution, and the current
	// frontier and source completion markers add twenty.
	const currentApplicationTables = 85 + 20
	if tableCount != currentApplicationTables {
		t.Fatalf("current schema contains %d application tables, want %d", tableCount, currentApplicationTables)
	}
	river, err := rivermigrate.New(riverdatabasesql.New(database.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := database.NewRaw(`INSERT INTO river_job(kind,args) VALUES ('baseline_rollback_probe','{}') RETURNING id`).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE unrelated_data(id integer PRIMARY KEY); INSERT INTO unrelated_data VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	var riverHistoryBefore int
	if err := database.NewRaw(`SELECT count(*) FROM river_migration`).Scan(ctx, &riverHistoryBefore); err != nil {
		t.Fatal(err)
	}
	if riverHistoryBefore == 0 {
		t.Fatal("River migration fixture has no history")
	}
	if _, err := application.Rollback(ctx); err != nil {
		t.Fatalf("roll back empty current application schema alongside River: %v", err)
	}
	var jobs, riverHistoryAfter, unrelated int
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE id=?`, jobID).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM river_migration`).Scan(ctx, &riverHistoryAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM unrelated_data WHERE id=7`).Scan(ctx, &unrelated); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || unrelated != 1 || riverHistoryAfter != riverHistoryBefore {
		t.Fatalf("application rollback changed other data: jobs=%d unrelated=%d River history=%d (was %d)", jobs, unrelated, riverHistoryAfter, riverHistoryBefore)
	}
	if _, err := application.Migrate(ctx); err != nil {
		t.Fatalf("reapply current application schema alongside River: %v", err)
	}
}
